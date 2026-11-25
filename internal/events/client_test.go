package events_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kirank55/kman/internal/events"
)

func TestClientOfReducesAUserAgentToAProductAndItsMajorVersion(t *testing.T) {
	for _, c := range []struct{ name, ua, want string }{
		// The User-Agents of the SDKs the e2e tests pin (e2e/testdata/sdk), as they
		// send them: TestClientOfKnowsThePinnedSDKs keeps these at the pinned versions.
		{"openai python", "OpenAI/Python 3.49.0", "openai-python/3"},
		{"anthropic python", "Anthropic/Python 2.8.0", "anthropic-python/2"},
		{"openai js", "OpenAI/JS 7.55.0", "openai-js/7"},
		{"anthropic js", "Anthropic/JS 1.8.0", "anthropic-js/1"},
		// Others an operator will meet.
		{"openai python, an older major", "OpenAI/Python 1.51.0", "openai-python/1"},
		{"anthropic js before 1.0", "Anthropic/JS 0.27.3", "anthropic-js/0"},
		{"curl", "curl/8.5.0", "curl/8"},
		{"curl, a bare major", "curl/8", "curl/8"},
		{"requests", "python-requests/2.31.0", "python-requests/2"},
		{"go", "Go-http-client/1.1", "go-http-client/1"},
		{"go, http2", "Go-http-client/2.0", "go-http-client/2"},
		{"okhttp", "okhttp/4.12.0", "okhttp/4"},
		{"node's own fetch", "node", "node"},
		{"deno", "Deno/1.40.2", "deno/1"},
		{"a browser", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", "mozilla/5"},
		{"postman", "PostmanRuntime/7.36.0", "postmanruntime/7"},
		{"a java client", "Apache-HttpClient/4.5.14 (Java/17.0.9)", "apache-httpclient/4"},
		{"a library on a runtime: the first product wins", "Python/3.11 aiohttp/3.9.1", "python/3"},
		{"a date version is not a major", "insomnia/20240101", "insomnia"},
		{"a date-like version, four digits", "Insomnia/2023.5.8", "insomnia/2023"},
		{"a product with a prerelease", "my-app/2.0.0-rc.1+build.7", "my-app/2"},
		{"a leading v on the version", "my-app/v3.1", "my-app"},
		{"an app", "my_app-worker/1.0.3", "my_app-worker/1"},
		{"no version", "MyApp", "myapp"},
		{"a version in words", "MyApp/latest", "myapp"},
		{"a name and a version, no slash", "Faraday v2.7.4", "faraday"},
		{"leading space", "  curl/8.5.0", "curl/8"},
		{"upper case becomes lower case", "CURL/8.5.0", "curl/8"},
		{"a trailing comment is dropped", "my-app/1.2 (build 77; contact ops@example.com)", "my-app/1"},
		{"a second product is dropped", "my-app/1.2 other/3", "my-app/1"},
		{"a bare slash", "my-app/", "my-app"},
		{"a digit in a name", "urllib3/2.0.7", "urllib3/2"},
		{"a short number in a name", "aws-sdk-go-v2/1.24.0", "aws-sdk-go-v2/1"},
		{"three digits in a name", "win386-tool/1", "win386-tool/1"},

		// No header at all is a client of its own.
		{"empty", "", "none"},
		{"only space", "   \t ", "none"},

		// Something was sent, and no product can be read from it.
		{"only a comment", "(compatible; MSIE 9.0)", "unknown"},
		{"starts with a digit", "123/4", "unknown"},
		{"starts with a symbol", "-curl/8", "unknown"},
		{"a control character first", "\x01curl/8", "unknown"},
		{"non-ASCII", "café/1", "unknown"},
		{"non-ASCII first", "ünïcode/1", "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := events.ClientOf(c.ua); got != c.want {
				t.Errorf("ClientOf(%q) = %q, want %q", c.ua, got, c.want)
			}
		})
	}
}

// What an app puts in its User-Agent that it should not have: addresses, mail,
// hostnames, keys and other secrets. None may survive, and what does is only a
// product token and a number.
func TestClientOfDropsWhateverAnAppPutInItsUserAgentThatItShouldNotHave(t *testing.T) {
	const vkey = "kmn_01HZXKJ3Q8EXAMPLEKEYID_7k2Qm9wXr4TnB8sVd1LpC6aYfH3uGeZ0j5oIc"
	for _, c := range []struct{ name, ua, want string }{
		{"a key alone", vkey, "unknown"},
		{"a key as a product", "kmn_abc123/1", "unknown"},
		{"a child key", "ek_kmn_abc123def", "unknown"},
		{"a key after a product", "my-app/1.0 " + vkey, "my-app/1"},
		{"a key as the version", "my-app/" + vkey, "my-app"},
		{"a key in a comment", "my-app/1.0 (key=" + vkey + ")", "my-app/1"},
		{"a key at the end of a name", "my-app-" + vkey + "/1", "unknown"},
		{"a key in upper case", strings.ToUpper(vkey), "unknown"},
		{"a key after a slash and a space", "Vendor/Tool " + vkey, "vendor"},
		{"a provider key as a product", "sk-proj-abcdEFGH1234ijklMNOP5678qrstUVWX", "unknown"},
		{"an Anthropic key as a product", "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789", "unknown"},
		{"a long hex token", "0123456789abcdef0123456789abcdef", "unknown"},
		{"an email", "alice@example.com", "unknown"},
		{"an email after a product", "my-app/1.0 alice@example.com", "my-app/1"},
		{"a hostname", "api.internal.example.com", "unknown"},
		{"a hostname with a version", "api.internal.example.com/3", "unknown"},
		{"an IPv4 address", "203.0.113.7", "unknown"},
		{"an IPv4 address after a product", "my-app/1.0 203.0.113.7", "my-app/1"},
		{"an IPv6 address", "2001:db8::1", "unknown"},
		{"a url", "https://example.com/hook?token=abc", "unknown"},
		{"a host and port", "my-host:8080/1", "unknown"},
		{"a path", "/usr/bin/my-app", "unknown"},
		{"a user and a host", "alice@laptop", "unknown"},
		{"a name followed by a colon", "user:password", "unknown"},
		// Short enough to pass as a name, and still not a product (#115).
		{"a host label with an address in it", "ip-10-0-3-7", "unknown"},
		{"a host label with a version", "ip-10-0-3-7/1", "unknown"},
		{"a host label after a product", "my-app/1.0 ip-10-0-3-7", "my-app/1"},
		{"a numbered host", "node-0042/2", "unknown"},
		{"a short token", "a1b2c3d4", "unknown"},
		{"a short token with a version", "x9k2/1", "unknown"},
		{"an EC2 host name", "ip-172-31-5-9", "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := events.ClientOf(c.ua)
			if got != c.want {
				t.Errorf("ClientOf(%q) = %q, want %q", c.ua, got, c.want)
			}
			for _, bad := range []string{"kmn_", "KMN_", "@", ":", "203.0.113", "example", "alice", "AbCdEf", "sk-"} {
				if strings.Contains(got, bad) {
					t.Errorf("ClientOf(%q) = %q, which holds %q", c.ua, got, bad)
				}
			}
		})
	}
}

func TestClientOfIsBoundedWhateverItIsGiven(t *testing.T) {
	for _, c := range []struct{ name, ua, want string }{
		{"a very long name is cut to 32 characters", strings.Repeat("a", 200), strings.Repeat("a", 32)},
		{"a first word that fills the window read is no product", strings.Repeat("a", 1000), "unknown"},
		{"nor is one that fills it before an email's @", strings.Repeat("a", 256) + "@example.com", "unknown"},
		{"a long name with a version keeps both inside 32", strings.Repeat("a", 100) + "/12.3", strings.Repeat("a", 29) + "/12"},
		{"a name just under the limit", strings.Repeat("a", 18) + "/1", strings.Repeat("a", 18) + "/1"},
		{"a long name with a short digit run still fits", strings.Repeat("b", 40) + "/1", strings.Repeat("b", 30) + "/1"},
		{"a huge version is no version", "my-app/" + strings.Repeat("9", 5000), "my-app"},
		{"a long run of spaces first", strings.Repeat(" ", 100000) + "curl/8", "unknown"},
		{"a very long run of product-like noise", strings.Repeat("a-", 100000), "unknown"},
		{"product-like noise inside the window", strings.Repeat("a-", 100) + "/1", strings.Repeat("a-", 15) + "/1"},
		{"a long name that holds digits is not a product", strings.Repeat("a1", 20), "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := events.ClientOf(c.ua)
			if got != c.want {
				t.Errorf("ClientOf(%.40q...) = %q, want %q", c.ua, got, c.want)
			}
			if len(got) > events.MaxClientLength {
				t.Errorf("a client of %d bytes, over the %d allowed", len(got), events.MaxClientLength)
			}
		})
	}
}

func TestClientOfHandlesControlCharactersAndBadEncodings(t *testing.T) {
	for _, c := range []struct{ name, ua, want string }{
		{"a null after the version", "curl/8\x00.5", "curl/8"},
		{"a newline after the version", "curl/8\r\nX-Injected: 1", "curl/8"},
		{"a null in the name", "cu\x00rl/8", "unknown"},
		{"a newline after the name", "curl\nX-Injected: 1", "unknown"},
		{"an escape sequence", "curl/8\x1b[31m", "curl/8"},
		{"a delete character", "curl/8\x7f", "curl/8"},
		{"a tab then a product", "\tcurl/8", "curl/8"},
		{"invalid UTF-8", "\xff\xfe\xfd", "unknown"},
		{"invalid UTF-8 after the product", "curl/8\xff\xfe", "curl/8"},
		{"a right-to-left override", "curl\u202e/8", "unknown"},
		{"a zero-width space in the name", "cu\u200brl/8", "unknown"},
		{"a bell", "\a", "unknown"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := events.ClientOf(c.ua); got != c.want {
				t.Errorf("ClientOf(%q) = %q, want %q", c.ua, got, c.want)
			}
		})
	}
}

var clientShape = regexp.MustCompile(`^(none|unknown|[a-z][a-z0-9_-]*(/[0-9]{1,4})?)$`)

// Whatever it is given, a client is a product token, a major version and nothing
// else, and never a key.
func FuzzClientOf(f *testing.F) {
	for _, s := range []string{
		"", "curl/8.5.0", "OpenAI/Python 3.49.0", "kmn_abc", "ek_kmn_abc", "a@b", "\x00", "\xff", "Mozilla/5.0 (X11)",
		strings.Repeat("x", 500), "my-app/1 kmn_zzz", "sk-ant-api03-xxxxxxxxxxxxxxxxxxxxxx", "a b c", "/", "///", "a/", "a/ 1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ua string) {
		got := events.ClientOf(ua)
		if !utf8.ValidString(got) || !clientShape.MatchString(got) {
			t.Fatalf("ClientOf(%q) = %q, which is not a product token and a major version", ua, got)
		}
		if len(got) > events.MaxClientLength {
			t.Fatalf("ClientOf(%q) = %q, over %d bytes", ua, got, events.MaxClientLength)
		}
		if strings.Contains(strings.ToLower(got), "kmn_") {
			t.Fatalf("ClientOf(%q) = %q, which holds a key", ua, got)
		}
		if events.ClientOf(ua) != got {
			t.Fatalf("ClientOf(%q) is not the same twice", ua)
		}
	})
}

// The SDKs' User-Agents above are the pinned versions' own. A bump of a pin in
// e2e/testdata/sdk fails here until the table is brought up to date, so the
// table cannot drift from what the e2e tests run.
func TestClientOfKnowsThePinnedSDKs(t *testing.T) {
	pins := map[string]string{
		"OpenAI/Python 3.49.0":   "openai-python/3",
		"Anthropic/Python 2.8.0": "anthropic-python/2",
		"OpenAI/JS 7.55.0":       "openai-js/7",
		"Anthropic/JS 1.8.0":     "anthropic-js/1",
	}
	for ua, want := range pins {
		if got := events.ClientOf(ua); got != want {
			t.Errorf("ClientOf(%q) = %q, want %q", ua, got, want)
		}
	}
	reqs, err := os.ReadFile("../../e2e/testdata/sdk/python/requirements.txt")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	raw, err := os.ReadFile("../../e2e/testdata/sdk/node/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	for _, line := range []struct{ found, pinned string }{
		{string(reqs), "openai==3.49.0"},
		{string(reqs), "anthropic==2.8.0"},
		{pkg.Dependencies["openai"], "7.55.0"},
		{pkg.Dependencies["@anthropic-ai/sdk"], "1.8.0"},
	} {
		if !strings.Contains(line.found, line.pinned) {
			t.Errorf("the pinned SDKs changed (no %q in e2e/testdata/sdk): update the User-Agents in this test to the new versions'", line.pinned)
		}
	}
}

// The reduction runs for every request, including a flood of made-up keys, so it
// must stay cheap: one allocation, the client itself.
func BenchmarkClientOf(b *testing.B) {
	for _, c := range []struct{ name, ua string }{
		{"sdk", "OpenAI/Python 3.49.0"},
		{"browser", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
		{"hostile", strings.Repeat("a", 100000)},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				events.ClientOf(c.ua)
			}
		})
	}
}
