package gateway

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
)

// forward sends the request to the upstream with the credential in place of the
// key, and passes the response back.
func (g *Gateway) forward(x *exchange) *refusal {
	r, up := x.r, x.route.Upstream
	// TRACE would echo the credential back to the app. CONNECT and OPTIONS *
	// have no path to forward.
	if r.Method == http.MethodTrace || r.Method == http.MethodConnect || !strings.HasPrefix(r.URL.Path, "/") {
		return &refusal{code: codePathNotAllowed}
	}
	out, err := http.NewRequestWithContext(r.Context(), r.Method, "", bytes.NewReader(x.body))
	if err != nil {
		return &refusal{code: codeUpstreamUnavailable}
	}
	out.URL = &url.URL{
		Scheme: scheme(up), Host: address(up, up.Host),
		// Only the path and the query come from the app: never a host.
		Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: r.URL.RawQuery, ForceQuery: r.URL.ForceQuery,
	}
	if up.ServerName != "" {
		out.Host = address(up, up.ServerName) // the name the upstream knows itself by
	}
	out.Header = outboundHeaders(r.Header)
	secret, ok := g.opts.Vault.Credential(x.route.Credential.ID)
	if !ok {
		g.opts.Log.Error("the vault does not hold a credential", "credential", x.route.Credential.ID, "key", x.route.Key.ID)
		return &refusal{code: codeUpstreamUnavailable}
	}
	profiles.For(up.Profile).Authorize(out.Header, up.Auth, secret)

	// RoundTrip and not http.Client, whose errors quote the URL and its query.
	resp, err := g.transportFor(up).RoundTrip(out)
	if err != nil {
		if r.Context().Err() != nil {
			x.abort = true // the app has gone: there's no one to answer
			return nil
		}
		g.opts.Log.Warn("the upstream is unavailable", "key", x.route.Key.ID, "upstream", up.Name, "err", err)
		return &refusal{code: codeUpstreamUnavailable}
	}
	defer resp.Body.Close()
	if !keepOnKman(resp, up) {
		g.opts.Log.Warn("blocked a redirect off the upstream", "key", x.route.Key.ID, "upstream", up.Name, "status", resp.StatusCode)
		return &refusal{code: codeUpstreamUnavailable, message: "The upstream answered with a redirect to somewhere else, which kman does not follow."}
	}
	g.respond(x, resp)
	return nil
}

// copyBuffers hold a chunk of a response on its way through.
var copyBuffers = sync.Pool{New: func() any { return new([32 << 10]byte) }}

// respond passes the upstream's response to the app as it arrives, so a stream
// stays a stream. If either side breaks off, the app's connection is cut, so
// that it can't take part of an answer for the whole.
func (g *Gateway) respond(x *exchange, resp *http.Response) {
	h := x.w.Header()
	copyResponseHeaders(h, resp.Header)
	if _, ok := resp.Header["Content-Type"]; !ok {
		h["Content-Type"] = nil // net/http would otherwise guess one
	}
	flusher := http.NewResponseController(x.w)
	x.w.WriteHeader(resp.StatusCode)
	flusher.Flush() // errors here show up at the next write

	buf := copyBuffers.Get().(*[32 << 10]byte)
	defer copyBuffers.Put(buf)
	for {
		n, err := resp.Body.Read(buf[:])
		if n > 0 {
			if _, werr := x.w.Write(buf[:n]); werr != nil {
				x.abort = true
				return
			}
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if x.r.Context().Err() == nil {
				g.opts.Log.Warn("the upstream broke off a response", "key", x.route.Key.ID, "upstream", x.route.Upstream.Name, "err", err)
			}
			x.abort = true
			return
		}
	}
	for name, values := range resp.Trailer {
		h[http.TrailerPrefix+name] = values
	}
}

func scheme(up *registry.Upstream) string {
	if up.TLS {
		return "https"
	}
	return "http"
}

// address is a host of the upstream's, with its port only when it isn't the
// scheme's own.
func address(up *registry.Upstream, host string) string {
	if (up.TLS && up.Port == 443) || (!up.TLS && up.Port == 80) {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(up.Port))
}
