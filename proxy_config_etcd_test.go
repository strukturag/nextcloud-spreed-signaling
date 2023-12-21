package signaling

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/server/v3/embed"
)

type TestProxyInformationEtcd struct {
	Address string `json:"address"`

	OtherData string `json:"otherdata,omitempty"`
}

func newProxyConfigEtcd(t *testing.T, proxy McuProxy) (*embed.Etcd, ProxyConfig) {
	t.Helper()
	etcd, client := NewEtcdClientForTest(t)
	cfg := goconf.NewConfigFile()
	cfg.AddOption("mcu", "keyprefix", "proxies/")
	p, err := NewProxyConfigEtcd(cfg, client, proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Stop()
	})
	return etcd, p
}

func SetEtcdProxy(t *testing.T, etcd *embed.Etcd, path string, proxy *TestProxyInformationEtcd) {
	t.Helper()
	data, err := json.Marshal(proxy)
	if err != nil {
		t.Fatal(err)
	}
	SetEtcdValue(etcd, path, data)
}

func TestProxyConfigEtcd(t *testing.T) {
	proxy := newMcuProxyForConfig(t)
	etcd, config := newProxyConfigEtcd(t, proxy)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	SetEtcdProxy(t, etcd, "proxies/a", &TestProxyInformationEtcd{
		Address: "https://foo/",
	})
	proxy.Expect("add", "https://foo/")
	if err := config.Start(); err != nil {
		t.Fatal(err)
	}
	proxy.WaitForEvents(ctx)

	proxy.Expect("add", "https://bar/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address: "https://bar/",
	})
	proxy.WaitForEvents(ctx)

	proxy.Expect("keep", "https://bar/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address:   "https://bar/",
		OtherData: "ignore-me",
	})
	proxy.WaitForEvents(ctx)

	proxy.Expect("remove", "https://foo/")
	DeleteEtcdValue(etcd, "proxies/a")
	proxy.WaitForEvents(ctx)

	proxy.Expect("remove", "https://bar/")
	proxy.Expect("add", "https://baz/")
	SetEtcdProxy(t, etcd, "proxies/b", &TestProxyInformationEtcd{
		Address: "https://baz/",
	})
	proxy.WaitForEvents(ctx)

	// Adding the same hostname multiple times should not trigger an event.
	SetEtcdProxy(t, etcd, "proxies/c", &TestProxyInformationEtcd{
		Address: "https://baz/",
	})
	time.Sleep(100 * time.Millisecond)
}
