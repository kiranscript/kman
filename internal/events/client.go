package events

import "strings"

// MaxClientLength is the most bytes a client has.
const MaxClientLength = 32

// Clients that are not a product.
const (
	// ClientNone is the client of a request that sent no User-Agent.
	ClientNone = "none"
	// ClientUnknown is the client of a request whose User-Agent holds no product
	// kman is willing to keep.
	ClientUnknown = "unknown"
)

const (
	// userAgentWindow bounds how much of a User-Agent is looked at, so that the
	// reduction costs the same however long an app's header is.
	userAgentWindow = 256
	// minSecretLength is how long a product name that holds a digit has to be to
	// read as a credential rather than a product: no product in use is named in
	// twenty characters with a digit in them, and a random token almost always has
	// both.
	minSecretLength = 20
	// maxMajorDigits bounds a major version: more is a date or a build number.
	maxMajorDigits = 4
	// maxWordLength bounds the word of an SDK's "Vendor/Word 1.2.3" form.
	maxWordLength = 16
	// maxNameDigitRuns and maxNameDigits bound the digits of a product name of
	// any length. A product names itself in words, with a digit or a short number
	// at most (urllib3, aws-sdk-go-v2, k6); a host label (ip-10-0-3-7, node-0042)
	// or a short token (a1b2c3) has several runs of digits, or a long one.
	maxNameDigitRuns = 1
	maxNameDigits    = 3
)

// ClientOf reduces a User-Agent to the client an event records (SPEC §9): its
// first product and that product's major version, in lower case, such as
// openai-python/3, curl/8 or python-requests/2. It is a pure function of its
// argument, and the one place a User-Agent is read; nothing else of the header is
// ever kept.
//
// What survives is only a name of letters, digits, '_' and '-' that starts with a
// letter, and a major version of one to four digits, in at most MaxClientLength
// bytes. Everything else an app put in its User-Agent is dropped: the minor and
// patch versions, the build, the comment in parentheses, every product after the
// first. A first word that is not a clean product name is not cut down to one:
// an address, a host name, an email, a URL, a key or a token is not a product,
// so the client is ClientUnknown, and the key of a virtual or child key (anything
// holding "kmn_") never survives. Nor does a name with more than one run of
// digits or more than three digits, as a host label or a short token has, or a
// first word that fills the whole window read. No User-Agent at all is
// ClientNone.
//
// An SDK that names itself "Vendor/Language 1.2.3", as the official OpenAI and
// Anthropic SDKs do, is read as vendor-language/1.
func ClientOf(userAgent string) string {
	ua := userAgent
	if len(ua) > userAgentWindow {
		ua = ua[:userAgentWindow]
	}
	i := 0
	for i < len(ua) && (ua[i] == ' ' || ua[i] == '\t') {
		i++
	}
	switch {
	case i == len(ua) && len(userAgent) <= userAgentWindow:
		return ClientNone
	case i == len(ua) || !isLetter(ua[i]):
		return ClientUnknown
	}
	start := i
	for i < len(ua) && isNameByte(ua[i]) {
		i++
	}
	name := ua[start:i]
	// A first word that runs to the end of the window cannot be seen to end, so
	// it cannot be told from the start of an email or a host name: no product is
	// named in 256 bytes.
	if i == len(ua) && len(userAgent) > userAgentWindow {
		return ClientUnknown
	}
	// The name has to be the whole of the word: "alice@example.com", "host:80" and
	// "api.example.com" are not products, and are not cut into one.
	if i < len(ua) {
		switch ua[i] {
		case ' ', '\t', '/', '(', ';', ',':
		default:
			return ClientUnknown
		}
	}
	if containsFold(name, "kmn_") {
		return ClientUnknown
	}
	if runs, n := digitRuns(name); runs > maxNameDigitRuns || n > maxNameDigits {
		return ClientUnknown
	}
	major, word := "", ""
	if i < len(ua) && ua[i] == '/' {
		major, word = versionAfterSlash(ua[i+1:])
		// A version that runs to the end of the window may be the start of a
		// longer number, which is no version.
		end := i + 1 + len(major)
		if word != "" {
			end += len(word) + 1
		}
		if major != "" && end == len(ua) && len(userAgent) > userAgentWindow {
			major, word = "", ""
		}
	}
	// A token is long and holds a digit: the name as it is recorded, with an
	// SDK's word, is what is judged, so that a recorded client is one again.
	product := len(name)
	if word != "" {
		product += 1 + len(word)
	}
	if product >= minSecretLength && strings.ContainsAny(name, "0123456789") {
		return ClientUnknown
	}
	var out [MaxClientLength]byte
	n := 0
	room := MaxClientLength
	if major != "" {
		room -= 1 + len(major)
	}
	put := func(s string) {
		for j := 0; j < len(s) && n < room; j++ {
			out[n] = lower(s[j])
			n++
		}
	}
	put(name)
	if word != "" {
		put("-")
		put(word)
	}
	if major != "" {
		out[n] = '/'
		n++
		n += copy(out[n:], major)
	}
	return string(out[:n])
}

// versionAfterSlash reads what follows "name/": a number, whose digits up to
// the first other character are the major version, or the SDK form "Word 1.2.3",
// which is a word of letters, one space and such a number.
func versionAfterSlash(s string) (major, word string) {
	if d := digits(s); d != "" {
		return majorOf(d), ""
	}
	j := 0
	for j < len(s) && j <= maxWordLength && isLetter(s[j]) {
		j++
	}
	if j == 0 || j > maxWordLength || j >= len(s) || s[j] != ' ' {
		return "", ""
	}
	if d := digits(s[j+1:]); d != "" {
		return majorOf(d), s[:j]
	}
	return "", ""
}

// majorOf is a run of digits as a major version, or nothing when it is too long
// to be one.
func majorOf(d string) string {
	if len(d) > maxMajorDigits {
		return ""
	}
	return d
}

// IsClient reports whether v is a client ClientOf records for some User-Agent,
// other than ClientNone and ClientUnknown: a product, in lower case, that passes
// every rule above, and a major version. ClientOf returns such a client
// unchanged, which is what this checks.
func IsClient(v string) bool {
	return v != "" && v != ClientNone && v != ClientUnknown && ClientOf(v) == v
}

// digitRuns counts the runs of digits in s, and its digits.
func digitRuns(s string) (runs, n int) {
	in := false
	for i := 0; i < len(s); i++ {
		d := s[i] >= '0' && s[i] <= '9'
		if d {
			n++
			if !in {
				runs++
			}
		}
		in = d
	}
	return runs, n
}

// digits is the run of digits s starts with.
func digits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func isLetter(b byte) bool   { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }
func isNameByte(b byte) bool { return isLetter(b) || b >= '0' && b <= '9' || b == '_' || b == '-' }

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// containsFold reports whether s holds the lower-case ASCII needle, in any case.
func containsFold(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if lower(s[i+j]) != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
