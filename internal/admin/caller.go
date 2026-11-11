package admin

import "context"

// Caller is who runs a command, and from where.
type Caller struct {
	// Actor is the admin token's name, or "system:<what>" for kman itself.
	Actor string
	// ClientIP is the address the request came from, "" if none (a Unix
	// socket, or in process).
	ClientIP string
}

type callerKey struct{}

// WithCaller returns ctx carrying the caller of the commands run with it.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// caller returns ctx's caller, or an unauthorized error if it has none.
func caller(ctx context.Context) (Caller, error) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	if !ok || c.Actor == "" {
		return Caller{}, errorf(CodeUnauthorized, "no caller: every admin command needs an actor")
	}
	return c, nil
}

// Whoami is the caller as the admin API sees it.
type Whoami struct {
	Actor string `json:"actor" doc:"The name of the admin token the request was made with."`
}

// Whoami returns the caller's actor, so a client can check its token.
func (s *Service) Whoami(ctx context.Context) (Whoami, error) {
	c, err := caller(ctx)
	if err != nil {
		return Whoami{}, err
	}
	return Whoami{Actor: c.Actor}, nil
}
