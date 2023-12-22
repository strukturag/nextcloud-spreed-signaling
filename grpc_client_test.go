/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
 *
 * @author Joachim Bauch <bauch@struktur.de>
 *
 * @license GNU AGPL version 3 or any later version
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */
package signaling

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"os"
	"path"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/server/v3/embed"
)

func (c *GrpcClients) getWakeupChannelForTesting() <-chan struct{} {
	if c.wakeupChanForTesting != nil {
		return c.wakeupChanForTesting
	}

	ch := make(chan struct{}, 1)
	c.wakeupChanForTesting = ch
	return ch
}

func NewGrpcClientsForTestWithConfig(t *testing.T, config *goconf.ConfigFile, etcdClient *EtcdClient) (*GrpcClients, *DnsMonitor) {
	dnsMonitor := newDnsMonitorForTest(t, time.Hour) // will be updated manually
	client, err := NewGrpcClients(config, etcdClient, dnsMonitor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	return client, dnsMonitor
}

func NewGrpcClientsForTest(t *testing.T, addr string) (*GrpcClients, *DnsMonitor) {
	config := goconf.NewConfigFile()
	config.AddOption("grpc", "targets", addr)
	config.AddOption("grpc", "dnsdiscovery", "true")

	return NewGrpcClientsForTestWithConfig(t, config, nil)
}

func NewGrpcClientsWithEtcdForTest(t *testing.T, etcd *embed.Etcd) (*GrpcClients, *DnsMonitor) {
	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().ListenClientUrls[0].String())

	config.AddOption("grpc", "targettype", "etcd")
	config.AddOption("grpc", "targetprefix", "/grpctargets")

	etcdClient, err := NewEtcdClient(config, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := etcdClient.Close(); err != nil {
			t.Error(err)
		}
	})

	return NewGrpcClientsForTestWithConfig(t, config, etcdClient)
}

func drainWakeupChannel(ch <-chan struct{}) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitForEvent(ctx context.Context, t *testing.T, ch <-chan struct{}) {
	t.Helper()

	select {
	case <-ch:
		return
	case <-ctx.Done():
		t.Error("timeout waiting for event")
	}
}

func Test_GrpcClients_EtcdInitial(t *testing.T) {
	_, addr1 := NewGrpcServerForTest(t)
	_, addr2 := NewGrpcServerForTest(t)

	etcd := NewEtcdForTest(t)

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	client, _ := NewGrpcClientsWithEtcdForTest(t, etcd)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitForInitialized(ctx); err != nil {
		t.Fatal(err)
	}

	if clients := client.GetClients(); len(clients) != 2 {
		t.Errorf("Expected two clients, got %+v", clients)
	}
}

func Test_GrpcClients_EtcdUpdate(t *testing.T) {
	etcd := NewEtcdForTest(t)
	client, _ := NewGrpcClientsWithEtcdForTest(t, etcd)
	ch := client.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if clients := client.GetClients(); len(clients) != 0 {
		t.Errorf("Expected no clients, got %+v", clients)
	}

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr2 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 2 {
		t.Errorf("Expected two clients, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	} else if clients[1].Target() != addr2 {
		t.Errorf("Expected target %s, got %s", addr2, clients[1].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/one")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr2 {
		t.Errorf("Expected target %s, got %s", addr2, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr3 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr3+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr3 {
		t.Errorf("Expected target %s, got %s", addr3, clients[0].Target())
	}
}

func Test_GrpcClients_EtcdIgnoreSelf(t *testing.T) {
	etcd := NewEtcdForTest(t)
	client, _ := NewGrpcClientsWithEtcdForTest(t, etcd)
	ch := client.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if clients := client.GetClients(); len(clients) != 0 {
		t.Errorf("Expected no clients, got %+v", clients)
	}

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	server2, addr2 := NewGrpcServerForTest(t)
	server2.serverId = GrpcServerId
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	waitForEvent(ctx, t, ch)
	client.selfCheckWaitGroup.Wait()
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/two")
	waitForEvent(ctx, t, ch)
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}
}

func Test_GrpcClients_DnsDiscovery(t *testing.T) {
	lookup := newMockDnsLookupForTest(t)
	target := "testgrpc:12345"
	ip1 := net.ParseIP("192.168.0.1")
	ip2 := net.ParseIP("192.168.0.2")
	targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
	targetWithIp2 := fmt.Sprintf("%s (%s)", target, ip2)
	lookup.Set("testgrpc", []net.IP{ip1})
	client, dnsMonitor := NewGrpcClientsForTest(t, target)
	ch := client.getWakeupChannelForTesting()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	dnsMonitor.checkHostnames()
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp1 {
		t.Errorf("Expected target %s, got %s", targetWithIp1, clients[0].Target())
	} else if !clients[0].ip.Equal(ip1) {
		t.Errorf("Expected IP %s, got %s", ip1, clients[0].ip)
	}

	lookup.Set("testgrpc", []net.IP{ip1, ip2})
	drainWakeupChannel(ch)
	dnsMonitor.checkHostnames()
	waitForEvent(ctx, t, ch)

	if clients := client.GetClients(); len(clients) != 2 {
		t.Errorf("Expected two client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp1 {
		t.Errorf("Expected target %s, got %s", targetWithIp1, clients[0].Target())
	} else if !clients[0].ip.Equal(ip1) {
		t.Errorf("Expected IP %s, got %s", ip1, clients[0].ip)
	} else if clients[1].Target() != targetWithIp2 {
		t.Errorf("Expected target %s, got %s", targetWithIp2, clients[1].Target())
	} else if !clients[1].ip.Equal(ip2) {
		t.Errorf("Expected IP %s, got %s", ip2, clients[1].ip)
	}

	lookup.Set("testgrpc", []net.IP{ip2})
	drainWakeupChannel(ch)
	dnsMonitor.checkHostnames()
	waitForEvent(ctx, t, ch)

	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp2 {
		t.Errorf("Expected target %s, got %s", targetWithIp2, clients[0].Target())
	} else if !clients[0].ip.Equal(ip2) {
		t.Errorf("Expected IP %s, got %s", ip2, clients[0].ip)
	}
}

func Test_GrpcClients_DnsDiscoveryInitialFailed(t *testing.T) {
	lookup := newMockDnsLookupForTest(t)
	target := "testgrpc:12345"
	ip1 := net.ParseIP("192.168.0.1")
	targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
	client, dnsMonitor := NewGrpcClientsForTest(t, target)
	ch := client.getWakeupChannelForTesting()

	testCtx, testCtxCancel := context.WithTimeout(context.Background(), testTimeout)
	defer testCtxCancel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.WaitForInitialized(ctx); err != nil {
		t.Fatal(err)
	}

	if clients := client.GetClients(); len(clients) != 0 {
		t.Errorf("Expected no client, got %+v", clients)
	}

	lookup.Set("testgrpc", []net.IP{ip1})
	drainWakeupChannel(ch)
	dnsMonitor.checkHostnames()
	waitForEvent(testCtx, t, ch)

	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp1 {
		t.Errorf("Expected target %s, got %s", targetWithIp1, clients[0].Target())
	} else if !clients[0].ip.Equal(ip1) {
		t.Errorf("Expected IP %s, got %s", ip1, clients[0].ip)
	}
}

func Test_GrpcClients_Encryption(t *testing.T) {
	serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	serverCert := GenerateSelfSignedCertificateForTesting(t, 1024, "Server cert", serverKey)
	clientCert := GenerateSelfSignedCertificateForTesting(t, 1024, "Testing client", clientKey)

	dir := t.TempDir()
	serverPrivkeyFile := path.Join(dir, "server-privkey.pem")
	serverPubkeyFile := path.Join(dir, "server-pubkey.pem")
	serverCertFile := path.Join(dir, "server-cert.pem")
	WritePrivateKey(serverKey, serverPrivkeyFile)          // nolint
	WritePublicKey(&serverKey.PublicKey, serverPubkeyFile) // nolint
	os.WriteFile(serverCertFile, serverCert, 0755)         // nolint
	clientPrivkeyFile := path.Join(dir, "client-privkey.pem")
	clientPubkeyFile := path.Join(dir, "client-pubkey.pem")
	clientCertFile := path.Join(dir, "client-cert.pem")
	WritePrivateKey(clientKey, clientPrivkeyFile)          // nolint
	WritePublicKey(&clientKey.PublicKey, clientPubkeyFile) // nolint
	os.WriteFile(clientCertFile, clientCert, 0755)         // nolint

	serverConfig := goconf.NewConfigFile()
	serverConfig.AddOption("grpc", "servercertificate", serverCertFile)
	serverConfig.AddOption("grpc", "serverkey", serverPrivkeyFile)
	serverConfig.AddOption("grpc", "clientca", clientCertFile)
	_, addr := NewGrpcServerForTestWithConfig(t, serverConfig)

	clientConfig := goconf.NewConfigFile()
	clientConfig.AddOption("grpc", "targets", addr)
	clientConfig.AddOption("grpc", "clientcertificate", clientCertFile)
	clientConfig.AddOption("grpc", "clientkey", clientPrivkeyFile)
	clientConfig.AddOption("grpc", "serverca", serverCertFile)
	clients, _ := NewGrpcClientsForTestWithConfig(t, clientConfig, nil)

	ctx, cancel1 := context.WithTimeout(context.Background(), time.Second)
	defer cancel1()

	if err := clients.WaitForInitialized(ctx); err != nil {
		t.Fatal(err)
	}

	for _, client := range clients.GetClients() {
		if _, err := client.GetServerId(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
