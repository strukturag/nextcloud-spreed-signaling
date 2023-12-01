package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/dlintw/goconf"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type proxyConfigEtcd struct {
	mu    sync.Mutex
	proxy McuProxy

	client    *EtcdClient
	keyPrefix string
	keyInfos  map[string]*ProxyInformationEtcd
	urlToKey  map[string]string
}

func NewProxyConfigEtcd(config *goconf.ConfigFile, etcdClient *EtcdClient, proxy McuProxy) (ProxyConfig, error) {
	if !etcdClient.IsConfigured() {
		return nil, errors.New("No etcd endpoints configured")
	}

	result := &proxyConfigEtcd{
		proxy: proxy,

		client:   etcdClient,
		keyInfos: make(map[string]*ProxyInformationEtcd),
		urlToKey: make(map[string]string),
	}
	if err := result.configure(config, false); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *proxyConfigEtcd) configure(config *goconf.ConfigFile, fromReload bool) error {
	keyPrefix, _ := config.GetString("mcu", "keyprefix")
	if keyPrefix == "" {
		keyPrefix = "/%s"
	}

	p.keyPrefix = keyPrefix
	return nil
}

func (p *proxyConfigEtcd) Start() error {
	p.client.AddListener(p)
	return nil
}

func (p *proxyConfigEtcd) Reload(config *goconf.ConfigFile) error {
	// not implemented
	return nil
}

func (p *proxyConfigEtcd) Stop() {
	p.client.RemoveListener(p)
}

func (p *proxyConfigEtcd) EtcdClientCreated(client *EtcdClient) {
	go func() {
		if err := client.Watch(context.Background(), p.keyPrefix, p, clientv3.WithPrefix()); err != nil {
			log.Printf("Error processing watch for %s: %s", p.keyPrefix, err)
		}
	}()

	go func() {
		client.WaitForConnection()

		waitDelay := initialWaitDelay
		for {
			response, err := p.getProxyUrls(client, p.keyPrefix)
			if err != nil {
				if err == context.DeadlineExceeded {
					log.Printf("Timeout getting initial list of proxy URLs, retry in %s", waitDelay)
				} else {
					log.Printf("Could not get initial list of proxy URLs, retry in %s: %s", waitDelay, err)
				}

				time.Sleep(waitDelay)
				waitDelay = waitDelay * 2
				if waitDelay > maxWaitDelay {
					waitDelay = maxWaitDelay
				}
				continue
			}

			for _, ev := range response.Kvs {
				p.EtcdKeyUpdated(client, string(ev.Key), ev.Value)
			}
			return
		}
	}()
}

func (p *proxyConfigEtcd) EtcdWatchCreated(client *EtcdClient, key string) {
}

func (p *proxyConfigEtcd) getProxyUrls(client *EtcdClient, keyPrefix string) (*clientv3.GetResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	return client.Get(ctx, keyPrefix, clientv3.WithPrefix())
}

func (p *proxyConfigEtcd) EtcdKeyUpdated(client *EtcdClient, key string, data []byte) {
	var info ProxyInformationEtcd
	if err := json.Unmarshal(data, &info); err != nil {
		log.Printf("Could not decode proxy information %s: %s", string(data), err)
		return
	}
	if err := info.CheckValid(); err != nil {
		log.Printf("Received invalid proxy information %s: %s", string(data), err)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	prev, found := p.keyInfos[key]
	if found && info.Address != prev.Address {
		// Address of a proxy has changed.
		p.removeEtcdProxyLocked(key)
		found = false
	}

	if otherKey, otherFound := p.urlToKey[info.Address]; otherFound && otherKey != key {
		log.Printf("Address %s is already registered for key %s, ignoring %s", info.Address, otherKey, key)
		return
	}

	if found {
		p.keyInfos[key] = &info
		p.proxy.KeepConnection(info.Address)
	} else {
		if err := p.proxy.AddConnection(false, info.Address); err != nil {
			log.Printf("Could not create proxy connection to %s: %s", info.Address, err)
			return
		}

		log.Printf("Added new connection to %s (from %s)", info.Address, key)
		p.keyInfos[key] = &info
		p.urlToKey[info.Address] = key
	}
}

func (p *proxyConfigEtcd) EtcdKeyDeleted(client *EtcdClient, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.removeEtcdProxyLocked(key)
}

func (p *proxyConfigEtcd) removeEtcdProxyLocked(key string) {
	info, found := p.keyInfos[key]
	if !found {
		return
	}

	delete(p.keyInfos, key)
	delete(p.urlToKey, info.Address)

	log.Printf("Removing connection to %s (from %s)", info.Address, key)
	p.proxy.RemoveConnection(info.Address)
}
