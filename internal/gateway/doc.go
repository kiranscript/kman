// Package gateway is the proxy listener's http.Handler. It owns the request pipeline:
// authenticate, policy, limit, reserve, forward, settle and emit, along with
// header stripping, redirect blocking and a bounded body buffer.
//
// It reads the registry's immutable snapshot and takes no locks (ADR 0006).
// It never imports admin.
package gateway
