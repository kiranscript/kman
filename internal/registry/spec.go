package registry

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"github.com/kirank55/kman/internal/policy"
)

// Auth styles.
const (
	AuthHeader = "header"
	AuthBasic  = "basic"
)

// Auth is how a credential is presented to an upstream.
type Auth struct {
	Style string
	// Header is the template of the header style, such as
	// "Authorization: Bearer {key}".
	Header string
	// BasicUsername is the username of the basic style. When it is empty,
	// the credential's secret is the whole user:password.
	BasicUsername string
}

// Spec is everything about an upstream that a preset supplies: where it is
// and how it authenticates.
type Spec struct {
	Profile     string
	Host        string
	Port        int
	TLS         bool
	ServerName  string // the TLS server name, when it is not Host
	Paths       []string
	InjectUsage bool
	Auth        Auth
	SDKEnv      string // the variable an SDK reads its key from
	SDKBasePath string // appended to kman's URL to make an SDK's base URL
}

// Preset is a built-in template for a well-known upstream. An upstream copies
// its Spec once, when it is added, and changes only when the operator syncs
// it. Version rises whenever the Spec changes.
type Preset struct {
	Name    string
	Title   string
	Version int
	Spec    Spec
}

// Profiles are the proxy's upstream profiles (SPEC §6).
const (
	ProfileOpenAI    = "openai"
	ProfileAnthropic = "anthropic"
	ProfileGeneric   = "generic"
)

// KeyPlaceholder is what a header template holds in place of the secret.
const KeyPlaceholder = "{key}"

var (
	hostChars = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	envName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	// headerName is an HTTP token (RFC 9110 §5.6.2).
	headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// forbiddenHeaders are the headers the proxy owns, which a credential may
// not be put in: the hop-by-hop headers it strips, and those that frame or
// route the request.
var forbiddenHeaders = []string{
	"connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
	"te", "trailer", "transfer-encoding", "upgrade", "host", "content-length",
}

// Validate reports the first thing wrong with s. Its message names the field
// and is safe to show: a Spec holds no secret.
func (s Spec) Validate() error {
	switch s.Profile {
	case ProfileOpenAI, ProfileAnthropic, ProfileGeneric:
	default:
		return fmt.Errorf("profile %q: use openai, anthropic or generic", s.Profile)
	}
	if err := checkHost(s.Host); err != nil {
		return fmt.Errorf("host %q: %w", s.Host, err)
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port %d: use 1 to 65535", s.Port)
	}
	if s.ServerName != "" {
		if !s.TLS {
			return errors.New("server name: it only applies with TLS")
		}
		if err := checkHost(s.ServerName); err != nil {
			return fmt.Errorf("server name %q: %w", s.ServerName, err)
		}
	}
	if len(s.Paths) == 0 {
		return errors.New("path allowlist: give at least one rule, such as \"POST /v1/things\" (\"* /**\" allows everything)")
	}
	for _, p := range s.Paths {
		if err := checkPathRule(p); err != nil {
			return fmt.Errorf("path allowlist rule %q: %w", p, err)
		}
	}
	if s.InjectUsage && s.Profile != ProfileOpenAI {
		return errors.New("inject usage: only the openai profile streams usage this way")
	}
	if err := s.Auth.validate(); err != nil {
		return err
	}
	if s.SDKEnv != "" && !envName.MatchString(s.SDKEnv) {
		return fmt.Errorf("SDK env %q: not an environment variable name", s.SDKEnv)
	}
	if b := s.SDKBasePath; b != "" && (!strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.ContainsAny(b, "?#\\ \t\r\n")) {
		return fmt.Errorf("SDK base path %q: use a path such as /v1, without a trailing slash", b)
	}
	return nil
}

func (a Auth) validate() error {
	switch a.Style {
	case AuthHeader:
		if a.BasicUsername != "" {
			return errors.New("basic username: it only applies to basic auth")
		}
		name, value, ok := strings.Cut(a.Header, ":")
		name = strings.TrimSpace(name)
		if !ok || !headerName.MatchString(name) {
			return fmt.Errorf("auth header %q: use the form \"Name: value {key}\"", a.Header)
		}
		if slices.Contains(forbiddenHeaders, strings.ToLower(name)) {
			return fmt.Errorf("auth header %q: kman owns the %s header", a.Header, name)
		}
		if strings.Count(value, KeyPlaceholder) != 1 {
			return fmt.Errorf("auth header %q: the value must hold {key} once", a.Header)
		}
		if strings.ContainsFunc(a.Header, isControl) {
			return errors.New("auth header: it holds a control character")
		}
	case AuthBasic:
		if a.Header != "" {
			return errors.New("auth header: it only applies to header auth")
		}
		if strings.Contains(a.BasicUsername, ":") || strings.ContainsFunc(a.BasicUsername, isControl) {
			return errors.New("basic username: it may not hold a colon or a control character")
		}
	default:
		return fmt.Errorf("auth style %q: use header or basic", a.Style)
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// checkHost accepts a host name or an IP address, with nothing else: no
// scheme, port, user or path.
func checkHost(h string) error {
	if h == "" || len(h) > 253 {
		return errors.New("use a host name or IP address of 1 to 253 characters")
	}
	if net.ParseIP(h) != nil || hostChars.MatchString(h) {
		return nil
	}
	return errors.New("use a host name or IP address, with no scheme, port or path")
}

// checkPathRule accepts "METHOD /path", the grammar policy.CheckPathRule
// owns and the proxy's matcher reads.
func checkPathRule(rule string) error { return policy.CheckPathRule(rule) }
