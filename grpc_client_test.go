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
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"go.etcd.io/etcd/server/v3/embed"
)

func NewGrpcClientsForTest(t *testing.T, addr string) *GrpcClients {
	config := goconf.NewConfigFile()
	config.AddOption("grpc", "targets", addr)
	config.AddOption("grpc", "dnsdiscovery", "true")

	client, err := NewGrpcClients(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func NewGrpcClientsWithEtcdForTest(t *testing.T, etcd *embed.Etcd) *GrpcClients {
	config := goconf.NewConfigFile()
	config.AddOption("etcd", "endpoints", etcd.Config().LCUrls[0].String())

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

	client, err := NewGrpcClients(config, etcdClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	return client
}

func drainWakeupChannel(ch chan bool) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func Test_GrpcClients_EtcdInitial(t *testing.T) {
	_, addr1 := NewGrpcServerForTest(t)
	_, addr2 := NewGrpcServerForTest(t)

	etcd := NewEtcdForTest(t)

	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))

	client := NewGrpcClientsWithEtcdForTest(t, etcd)
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
	client := NewGrpcClientsWithEtcdForTest(t, etcd)
	ch := make(chan bool, 1)
	client.wakeupChanForTesting = ch

	if clients := client.GetClients(); len(clients) != 0 {
		t.Errorf("Expected no clients, got %+v", clients)
	}

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr2 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	<-ch
	if clients := client.GetClients(); len(clients) != 2 {
		t.Errorf("Expected two clients, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	} else if clients[1].Target() != addr2 {
		t.Errorf("Expected target %s, got %s", addr2, clients[1].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/one")
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr2 {
		t.Errorf("Expected target %s, got %s", addr2, clients[0].Target())
	}

	drainWakeupChannel(ch)
	_, addr3 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr3+"\"}"))
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr3 {
		t.Errorf("Expected target %s, got %s", addr3, clients[0].Target())
	}
}

func Test_GrpcClients_EtcdIgnoreSelf(t *testing.T) {
	etcd := NewEtcdForTest(t)
	client := NewGrpcClientsWithEtcdForTest(t, etcd)
	ch := make(chan bool, 1)
	client.wakeupChanForTesting = ch

	if clients := client.GetClients(); len(clients) != 0 {
		t.Errorf("Expected no clients, got %+v", clients)
	}

	drainWakeupChannel(ch)
	_, addr1 := NewGrpcServerForTest(t)
	SetEtcdValue(etcd, "/grpctargets/one", []byte("{\"address\":\""+addr1+"\"}"))
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	server2, addr2 := NewGrpcServerForTest(t)
	server2.serverId = GrpcServerId
	SetEtcdValue(etcd, "/grpctargets/two", []byte("{\"address\":\""+addr2+"\"}"))
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}

	drainWakeupChannel(ch)
	DeleteEtcdValue(etcd, "/grpctargets/two")
	<-ch
	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != addr1 {
		t.Errorf("Expected target %s, got %s", addr1, clients[0].Target())
	}
}

func Test_GrpcClients_DnsDiscovery(t *testing.T) {
	var ipsResult []net.IP
	lookupGrpcIp = func(host string) ([]net.IP, error) {
		if host == "testgrpc" {
			return ipsResult, nil
		}

		return nil, fmt.Errorf("unknown host")
	}
	target := "testgrpc:12345"
	ip1 := net.ParseIP("192.168.0.1")
	ip2 := net.ParseIP("192.168.0.2")
	targetWithIp1 := fmt.Sprintf("%s (%s)", target, ip1)
	targetWithIp2 := fmt.Sprintf("%s (%s)", target, ip2)
	ipsResult = []net.IP{ip1}
	client := NewGrpcClientsForTest(t, target)
	ch := make(chan bool, 1)
	client.wakeupChanForTesting = ch

	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp1 {
		t.Errorf("Expected target %s, got %s", targetWithIp1, clients[0].Target())
	} else if !clients[0].ip.Equal(ip1) {
		t.Errorf("Expected IP %s, got %s", ip1, clients[0].ip)
	}

	ipsResult = []net.IP{ip1, ip2}
	drainWakeupChannel(ch)
	client.updateGrpcIPs()
	<-ch

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

	ipsResult = []net.IP{ip2}
	drainWakeupChannel(ch)
	client.updateGrpcIPs()
	<-ch

	if clients := client.GetClients(); len(clients) != 1 {
		t.Errorf("Expected one client, got %+v", clients)
	} else if clients[0].Target() != targetWithIp2 {
		t.Errorf("Expected target %s, got %s", targetWithIp2, clients[0].Target())
	} else if !clients[0].ip.Equal(ip2) {
		t.Errorf("Expected IP %s, got %s", ip2, clients[0].ip)
	}
}
