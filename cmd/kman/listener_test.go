package main

import (
	"log/slog"
	"net/http"
	"testing"
)

// The security review, finding S-1: a listener that never gives up on an idle
// or half-sent connection can be filled with them. Both listeners bound how long
// a head takes and how long a kept-alive connection may sit idle. (A request's
// body has its own bounds, in the gateway.)
func TestBothListenersBoundASlowOrIdleConnection(t *testing.T) {
	cfg := Config{ProxyListen: "127.0.0.1:0", AdminListen: "127.0.0.1:0"}
	ls, err := listen(cfg, slog.New(slog.DiscardHandler), http.NotFoundHandler(), http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		l.l.Close()
		if l.srv.ReadHeaderTimeout <= 0 || l.srv.IdleTimeout <= 0 {
			t.Errorf("%s listener: ReadHeaderTimeout %v, IdleTimeout %v: want both set", l.name, l.srv.ReadHeaderTimeout, l.srv.IdleTimeout)
		}
		if l.srv.MaxHeaderBytes <= 0 || l.srv.MaxHeaderBytes > 1<<20 {
			t.Errorf("%s listener: MaxHeaderBytes %d", l.name, l.srv.MaxHeaderBytes)
		}
	}
}
