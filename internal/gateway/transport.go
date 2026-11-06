package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"github.com/kirank55/kman/internal/registry"
)

// newTransport is how kman reaches upstreams. It uses no ambient proxy, because
// an HTTP_PROXY in the environment must not get to see credentials (SPEC §6). It
// never follows a redirect, as no RoundTripper does. It sets no response-header
// timeout, because a long completion is slow to start. A serverName is the name
// TLS checks when it isn't the host being dialled.
func newTransport(roots *x509.CertPool, serverName string) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true, // a custom TLS config turns it off otherwise
		DisableCompression:  true, // the app gets the encoding it asked for, or none
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256, // SPEC §17's load shouldn't redial for each request
		IdleConnTimeout:     90 * time.Second,
	}
}

// transportFor is the transport for an upstream. Those with a server name of
// their own need a transport of their own, since it is part of the TLS config. A
// sync.Map read takes no lock once the name has been seen.
func (g *Gateway) transportFor(up *registry.Upstream) http.RoundTripper {
	if g.opts.Transport != nil {
		return g.opts.Transport
	}
	if up.ServerName == "" || !up.TLS {
		return g.base
	}
	if t, ok := g.named.Load(up.ServerName); ok {
		return t.(*http.Transport)
	}
	t, _ := g.named.LoadOrStore(up.ServerName, newTransport(g.opts.RootCAs, up.ServerName))
	return t.(*http.Transport)
}
