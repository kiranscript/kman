package registry_test

import (
	"strings"
	"testing"

	"github.com/kirank55/kman/internal/registry"
)

func goodSpec() registry.Spec {
	return registry.Spec{
		Profile: "generic", Host: "api.example.com", Port: 443, TLS: true,
		Paths: []string{"POST /v1/things", "GET /v1/things/*", "* /v2/**"},
		Auth:  registry.Auth{Style: registry.AuthHeader, Header: "Authorization: Bearer {key}"},
	}
}

func TestAGoodSpecValidates(t *testing.T) {
	for name, change := range map[string]func(*registry.Spec){
		"as is":          func(*registry.Spec) {},
		"an IPv4 host":   func(s *registry.Spec) { s.Host = "127.0.0.1" },
		"an IPv6 host":   func(s *registry.Spec) { s.Host = "::1" },
		"a server name":  func(s *registry.Spec) { s.ServerName = "internal.example.com" },
		"plain HTTP":     func(s *registry.Spec) { s.TLS, s.Port = false, 8080 },
		"openai profile": func(s *registry.Spec) { s.Profile, s.InjectUsage = "openai", true },
		"anthropic":      func(s *registry.Spec) { s.Profile = "anthropic" },
		"an sdk env":     func(s *registry.Spec) { s.SDKEnv, s.SDKBasePath = "EXAMPLE_API_KEY", "/api/v1" },
		"another header": func(s *registry.Spec) { s.Auth.Header = "x-api-key: {key}" },
		"a bare key":     func(s *registry.Spec) { s.Auth.Header = "Authorization: {key}" },
		"basic":          func(s *registry.Spec) { s.Auth = registry.Auth{Style: registry.AuthBasic} },
		"basic and user": func(s *registry.Spec) { s.Auth = registry.Auth{Style: registry.AuthBasic, BasicUsername: "api"} },
	} {
		s := goodSpec()
		change(&s)
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestABadSpecIsRefusedAndTheErrorNamesTheField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*registry.Spec)
		want   string
	}{
		{"unknown profile", func(s *registry.Spec) { s.Profile = "gemini" }, "profile"},
		{"no host", func(s *registry.Spec) { s.Host = "" }, "host"},
		{"a URL for a host", func(s *registry.Spec) { s.Host = "https://api.example.com" }, "host"},
		{"a path in the host", func(s *registry.Spec) { s.Host = "api.example.com/v1" }, "host"},
		{"a port in the host", func(s *registry.Spec) { s.Host = "api.example.com:443" }, "host"},
		{"user info in the host", func(s *registry.Spec) { s.Host = "me@api.example.com" }, "host"},
		{"a space in the host", func(s *registry.Spec) { s.Host = "api example.com" }, "host"},
		{"a long host", func(s *registry.Spec) { s.Host = strings.Repeat("a", 254) }, "host"},
		{"port 0", func(s *registry.Spec) { s.Port = 0 }, "port"},
		{"port too big", func(s *registry.Spec) { s.Port = 65536 }, "port"},
		{"a bad server name", func(s *registry.Spec) { s.ServerName = "a/b" }, "server name"},
		{"a server name without TLS", func(s *registry.Spec) { s.TLS, s.ServerName = false, "a.example.com" }, "server name"},
		{"no paths", func(s *registry.Spec) { s.Paths = nil }, "path"},
		{"a path without a method", func(s *registry.Spec) { s.Paths = []string{"/v1/x"} }, "path"},
		{"a relative path", func(s *registry.Spec) { s.Paths = []string{"GET v1/x"} }, "path"},
		{"a lower-case method", func(s *registry.Spec) { s.Paths = []string{"get /v1/x"} }, "path"},
		{"** in the middle", func(s *registry.Spec) { s.Paths = []string{"GET /v1/**/x"} }, "path"},
		{"a partial wildcard", func(s *registry.Spec) { s.Paths = []string{"GET /v1/x*"} }, "path"},
		{"a dot segment", func(s *registry.Spec) { s.Paths = []string{"GET /v1/../admin"} }, "path"},
		{"a line break in a path", func(s *registry.Spec) { s.Paths = []string{"GET /v1/x\nPOST /y"} }, "path"},
		{"inject usage off the openai profile", func(s *registry.Spec) { s.InjectUsage = true }, "inject"},
		{"no auth style", func(s *registry.Spec) { s.Auth = registry.Auth{} }, "auth"},
		{"an unknown auth style", func(s *registry.Spec) { s.Auth = registry.Auth{Style: "oauth"} }, "auth"},
		{"a header without {key}", func(s *registry.Spec) { s.Auth.Header = "Authorization: Bearer" }, "{key}"},
		{"a header with {key} twice", func(s *registry.Spec) { s.Auth.Header = "X: {key} {key}" }, "{key}"},
		{"a header without a name", func(s *registry.Spec) { s.Auth.Header = "{key}" }, "header"},
		{"a bad header name", func(s *registry.Spec) { s.Auth.Header = "Bad Name: {key}" }, "header"},
		{"a line break in a header", func(s *registry.Spec) { s.Auth.Header = "X: {key}\r\nY: z" }, "header"},
		{"a hop-by-hop header", func(s *registry.Spec) { s.Auth.Header = "Connection: {key}" }, "header"},
		{"the Host header", func(s *registry.Spec) { s.Auth.Header = "Host: {key}" }, "header"},
		{"a basic username on the header style", func(s *registry.Spec) { s.Auth.BasicUsername = "api" }, "username"},
		{"a header on the basic style", func(s *registry.Spec) { s.Auth.Style = registry.AuthBasic }, "header"},
		{"a colon in a basic username", func(s *registry.Spec) {
			s.Auth = registry.Auth{Style: registry.AuthBasic, BasicUsername: "a:b"}
		}, "username"},
		{"a bad sdk env", func(s *registry.Spec) { s.SDKEnv = "NOT A NAME" }, "SDK env"},
		{"a relative sdk base path", func(s *registry.Spec) { s.SDKBasePath = "v1" }, "SDK base path"},
		{"a trailing slash on the sdk base path", func(s *registry.Spec) { s.SDKBasePath = "/v1/" }, "SDK base path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := goodSpec()
			tc.change(&s)
			err := s.Validate()
			if err == nil {
				t.Fatal("it validated")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
