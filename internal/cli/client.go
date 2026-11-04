package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/admin"
)

// client is the admin API's one typed client: admin.API over /admin/v1.
// The server names the actor from the token, so the caller in ctx is not
// sent.
type client struct {
	base  string // such as http://127.0.0.1:8081, or http://kman for a socket
	shown string // the admin URL as the operator gave it
	token string
	hc    *http.Client
}

var _ admin.API = (*client)(nil)

const prefix = "/admin/v1"

// newClient returns a client for an admin URL: http://host:port,
// https://host:port, or unix:/abs/path for a Unix socket.
func newClient(adminURL, token string) (*client, error) {
	// No proxy: an admin token goes only where the operator pointed it.
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, IdleConnTimeout: 30 * time.Second}
	c := &client{shown: adminURL, token: token, hc: &http.Client{Transport: tr, Timeout: 30 * time.Second}}
	if path, ok := strings.CutPrefix(adminURL, "unix:"); ok {
		if !strings.HasPrefix(path, "/") && !isWindowsAbs(path) {
			return nil, fmt.Errorf("admin URL %q: a socket path must be absolute", adminURL)
		}
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
		c.base = "http://kman"
		return c, nil
	}
	u, err := url.Parse(adminURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("admin URL %q: use http://host:port, https://host:port or unix:/path/to/socket", adminURL)
	}
	c.base = u.Scheme + "://" + u.Host
	return c, nil
}

// isWindowsAbs reports whether p looks like C:\... or C:/...
func isWindowsAbs(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// do sends one request and decodes the response into out. An error the
// server explains is returned as an *admin.Error.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	u := c.base + prefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach kman at %s (is kman serve running?): %w", c.shown, unwrapURLError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("read the response from %s: %w", c.shown, err)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error admin.Error `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			return &e.Error
		}
		return fmt.Errorf("%s answered %s: is it a kman admin listener?", c.shown, resp.Status)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s answered what is not the admin API's JSON: %w", c.shown, err)
	}
	return nil
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (c *client) Whoami(ctx context.Context) (admin.Whoami, error) {
	var out admin.Whoami
	return out, c.do(ctx, "GET", "/whoami", nil, nil, &out)
}

func (c *client) CreateAdminToken(ctx context.Context, req admin.CreateAdminTokenRequest) (admin.CreatedAdminToken, error) {
	var out admin.CreatedAdminToken
	return out, c.do(ctx, "POST", "/admin-tokens", nil, req, &out)
}

func (c *client) ListAdminTokens(ctx context.Context) (admin.AdminTokenList, error) {
	var out admin.AdminTokenList
	return out, c.do(ctx, "GET", "/admin-tokens", nil, nil, &out)
}

func (c *client) RotateAdminToken(ctx context.Context, req admin.AdminTokenName) (admin.CreatedAdminToken, error) {
	var out admin.CreatedAdminToken
	return out, c.do(ctx, "POST", "/admin-tokens/"+url.PathEscape(req.Name)+"/rotate", nil, nil, &out)
}

func (c *client) RevokeAdminToken(ctx context.Context, req admin.AdminTokenName) (admin.AdminToken, error) {
	var out admin.AdminToken
	return out, c.do(ctx, "POST", "/admin-tokens/"+url.PathEscape(req.Name)+"/revoke", nil, nil, &out)
}

func (c *client) Settings(ctx context.Context) (admin.Settings, error) {
	var out admin.Settings
	return out, c.do(ctx, "GET", "/settings", nil, nil, &out)
}

func (c *client) UpdateSettings(ctx context.Context, req admin.SettingsUpdate) (admin.Settings, error) {
	var out admin.Settings
	return out, c.do(ctx, "PATCH", "/settings", nil, req, &out)
}

func (c *client) Audit(ctx context.Context, req admin.AuditQuery) (admin.AuditPage, error) {
	q := url.Values{}
	if req.After != 0 {
		q.Set("after", strconv.FormatInt(req.After, 10))
	}
	if req.Limit != 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Latest {
		q.Set("latest", "true")
	}
	var out admin.AuditPage
	return out, c.do(ctx, "GET", "/audit", q, nil, &out)
}

// connect returns a client logged in from the credentials file or the
// environment.
func (c *call) connect() (*client, login, error) {
	l, err := c.login()
	if err != nil {
		return nil, login{}, err
	}
	cl, err := newClient(l.url, l.token)
	return cl, l, err
}

// explain adds what to do to an error from the server.
func (c *call) explain(err error, l login) error {
	var ae *admin.Error
	if errors.As(err, &ae) && ae.Code == admin.CodeUnauthorized {
		return fmt.Errorf("%s (the token from %s): run kman login --admin-url %s with a live token", ae.Message, l.tokenFrom, l.url)
	}
	return err
}

// printJSON prints v as indented JSON.
func (c *call) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = c.env.Stdout.Write(append(b, '\n'))
	return err
}
