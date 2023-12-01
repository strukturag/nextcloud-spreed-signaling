package signaling

import (
	"net"

	"github.com/dlintw/goconf"
)

var (
	lookupProxyIP = net.LookupIP
)

type ProxyConfig interface {
	Start() error
	Stop()

	Reload(config *goconf.ConfigFile) error
}
