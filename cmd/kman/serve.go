package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/admin"
	"github.com/kirank55/kman/internal/adminapi"
	"github.com/kirank55/kman/internal/gateway"
	"github.com/kirank55/kman/internal/policy"
	"github.com/kirank55/kman/internal/pricing"
	"github.com/kirank55/kman/internal/profiles"
	"github.com/kirank55/kman/internal/registry"
	"github.com/kirank55/kman/internal/store"
	"github.com/kirank55/kman/internal/vault"
)

// shutdownGrace is how long serve waits for requests in flight once asked
// to stop.
const shutdownGrace = 10 * time.Second

// cmdServe runs kman: it reads kman.yaml once, opens the store, unwraps the
// data key, and serves the proxy and admin listeners until ctx ends.
func cmdServe(ctx context.Context, args []string, s *sys) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(s.stderr)
	configDir := fs.String("config-dir", "", "the config directory holding kman.yaml (default ~/.config/kman, or %APPDATA%\\kman on Windows)")
	allowDebug := fs.Bool("allow-debug", false, "skip the process hardening that keeps the master key out of core dumps and debuggers")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(s.stderr, "usage: kman serve [--config-dir dir] [--allow-debug]")
		return 1
	}
	log := slog.New(slog.NewTextHandler(s.stderr, nil))
	if err := serve(ctx, s, log, *configDir, vault.Options{AllowDebug: *allowDebug}); err != nil {
		fmt.Fprintln(s.stderr, "kman serve:", err)
		return 1
	}
	// Logged once the vault is wiped and the store closed.
	log.Info("stopped")
	return 0
}

func serve(ctx context.Context, s *sys, log *slog.Logger, configDir string, opts vault.Options) error {
	if configDir == "" {
		dir, _, err := defaultDirs(s.goos, s.getenv)
		if err != nil {
			return err
		}
		configDir = dir
	}
	cfg, err := loadConfig(filepath.Join(configDir, configFile), s.getenv)
	if err != nil {
		return err
	}
	// serve never initialises an empty data directory: store.Open would.
	if _, err := os.Stat(filepath.Join(cfg.DataDir, store.FileName)); err != nil {
		return fmt.Errorf("%s holds no kman database (%v): run kman init to set up an install; serve never creates one", cfg.DataDir, err)
	}
	if cfg.MasterKeyFile != "" {
		within, err := inside(cfg.DataDir, cfg.MasterKeyFile)
		if err != nil {
			return err
		}
		if within {
			if !cfg.AllowKeyInDataDir {
				return fmt.Errorf("%s: %w. Move it out, or set allow_key_in_data_dir: true if you accept that", cfg.MasterKeyFile, errKeyInDataDir)
			}
			log.Warn("the master key file is inside the data directory (allow_key_in_data_dir is set)", "path", cfg.MasterKeyFile)
		}
	}
	src, err := keySource(cfg, s.stderr, func() ([]byte, error) {
		return s.passphrase("kman: passphrase for " + cfg.MasterKeyFile + ": ")
	})
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, store.Options{Dir: cfg.DataDir, Now: s.now(), Logger: log})
	if err != nil {
		return err
	}
	defer st.Close()
	var dk store.DataKey
	if err := st.Do(ctx, func(tx *store.Tx) error {
		dk, err = tx.CurrentDataKey()
		return err
	}); err != nil {
		return err
	}
	v, err := vault.Unlock(src, vault.WrappedKey{ID: dk.ID, Nonce: dk.Nonce, Wrapped: dk.Wrapped, Fingerprint: dk.KEKFingerprint}, opts)
	if err != nil {
		return err
	}
	defer v.Close()

	// The meter (#41) will read prices on the request path.
	reg := registry.New()
	prices := pricing.NewPrices(pricing.Builtin())
	svc := admin.New(admin.Options{Store: st, Now: s.now, Vault: v, Presets: profiles.Presets(), Apply: reg.Apply, Prices: builtinPrices,
		ApplyPrices: publishOverrides(prices), NormalizePolicy: policy.Normalize})
	if err := svc.Load(ctx); err != nil {
		return err
	}
	proxy := gateway.New(gateway.Options{Routes: reg, Vault: v, Now: s.now, Log: log})
	ls, err := listen(cfg, log, proxy, adminHandler(svc, log))
	if err != nil {
		return err
	}
	errs := make(chan error, len(ls))
	for _, l := range ls {
		go func() { errs <- l.serve() }()
	}
	log.Info("kman is running")

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err = <-errs:
		log.Error("a listener failed; shutting down", "err", err)
	}
	if s.stopSignals != nil {
		s.stopSignals() // a second signal ends the process at once
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	for _, l := range ls {
		if serr := l.srv.Shutdown(shutdownCtx); serr != nil {
			log.Error("shutdown", "listener", l.name, "err", serr)
		}
	}
	return err
}

// listener is one bound listener and the server for it.
type listener struct {
	name string
	l    net.Listener
	srv  *http.Server
	tls  bool
}

func (l *listener) serve() error {
	var err error
	if l.tls {
		err = l.srv.ServeTLS(l.l, "", "")
	} else {
		err = l.srv.Serve(l.l)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("%s listener: %w", l.name, err)
}

// listen binds the proxy and admin listeners, or neither.
func listen(cfg Config, log *slog.Logger, proxyH, adminH http.Handler) ([]*listener, error) {
	proxy, err := newListener("proxy", cfg.ProxyListen, cfg.TLS.Proxy, proxyH, log)
	if err != nil {
		return nil, err
	}
	adm, err := newListener("admin", cfg.AdminListen, cfg.TLS.Admin, adminH, log)
	if err != nil {
		proxy.l.Close()
		return nil, err
	}
	return []*listener{proxy, adm}, nil
}

// adminHandler serves the admin listener: the admin API under /admin/v1,
// and nothing else until the dashboard (M3) and /metrics (#46).
func adminHandler(svc *admin.Service, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(adminapi.Prefix+"/", adminapi.Handler(svc, log))
	mux.Handle("/", notYet("the admin listener"))
	return mux
}

func newListener(name, addr string, t ListenerTLS, h http.Handler, log *slog.Logger) (*listener, error) {
	var tc *tls.Config
	if t.enabled() {
		cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("tls.%s: %w", name, err)
		}
		tc = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}
	var l net.Listener
	var err error
	shown := addr
	if path, ok := strings.CutPrefix(addr, unixPrefix); ok {
		l, err = listenUnix(path)
	} else {
		l, err = net.Listen("tcp", addr)
		if err == nil {
			shown = l.Addr().String()
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s listener: %w", name, err)
	}
	log.Info(name+" listening", "addr", shown, "tls", tc != nil)
	srv := &http.Server{
		Handler:           h,
		TLSConfig:         tc,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return &listener{name: name, l: l, srv: srv, tls: tc != nil}, nil
}

// listenUnix binds a Unix socket that only this user can connect to. A
// socket left behind by a kman that did not stop cleanly is replaced;
// any other file at path is not.
func listenUnix(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := listenUnixPrivate(path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, fileMode); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// notYet answers every request that nothing on the admin listener serves yet.
func notYet(what string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "{\"error\":{\"code\":\"not_found\",\"message\":\"%s serves nothing here yet\"}}\n", what)
	})
}
