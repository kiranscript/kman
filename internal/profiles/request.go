package profiles

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Asked is what a request says about what it will cost, read from its body
// before it is forwarded.
type Asked struct {
	// Model is the request's top-level model, and Metered says there is one: a
	// request without it, or with anything but a non-empty string, can't be priced
	// (SPEC §8: an unmetered request).
	Model   string
	Metered bool
	// MaxTokens is the most output the request allows itself, or zero when it says
	// nothing usable. It is the largest of the limits it states, so that stating
	// two of them can't shrink a reservation.
	MaxTokens   int64
	ServiceTier string // the tier asked for; Anthropic's fast mode is the "fast" tier
}

// ask reads the top-level fields of a JSON request body. Only the top level
// counts: what the app wrote under messages or input is its content, and no
// field. It holds none of the body, however large, and checks no more of the
// JSON than it must to find those fields: a body that isn't valid JSON is
// refused by the upstream, which charges nothing for it.
func ask(body []byte) Asked {
	var a Asked
	var model string
	var haveModel bool
	eachField(body, func(key string, value []byte) {
		switch key {
		case "model":
			model, haveModel = stringValue(value)
		case "max_tokens", "max_completion_tokens", "max_output_tokens":
			a.MaxTokens = max(a.MaxTokens, tokenLimit(value))
		case "service_tier":
			if v, ok := stringValue(value); ok {
				a.ServiceTier = v
			}
		case "speed":
			if v, _ := stringValue(value); v == "fast" {
				a.ServiceTier = "fast"
			}
		}
	})
	a.Model, a.Metered = model, haveModel && model != ""
	return a
}

// stringValue is the string a JSON value holds, and false for anything else.
func stringValue(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' {
		return "", false
	}
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1]), true
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// tokenLimit is a limit on output as a whole number of tokens, rounded up, or
// zero for anything that isn't a sensible one.
func tokenLimit(raw []byte) int64 {
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f <= 0 || f > maxCount {
		return 0
	}
	return int64(math.Ceil(f))
}

// eachField calls visit with each member of a JSON object's top level, its key
// unescaped and its raw value, in order and repeats included. For anything that
// isn't an object it calls nothing. A value is a slice of body.
func eachField(body []byte, visit func(key string, value []byte)) {
	i := skipSpace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return
	}
	i++
	for {
		i = skipSpace(body, i)
		if i >= len(body) || body[i] != '"' {
			return
		}
		end := endOfString(body, i)
		if end < 0 {
			return
		}
		key, ok := stringValue(body[i : end+1])
		if !ok {
			return
		}
		i = skipSpace(body, end+1)
		if i >= len(body) || body[i] != ':' {
			return
		}
		i = skipSpace(body, i+1)
		end = endOfValue(body, i)
		if end < 0 {
			return
		}
		visit(key, body[i:end])
		i = skipSpace(body, end)
		if i >= len(body) || body[i] != ',' {
			return
		}
		i++
	}
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// endOfString is the index of the quote that closes the string opening at i, or
// -1 when there isn't one.
func endOfString(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// endOfValue is the index just past the JSON value starting at i, or -1.
func endOfValue(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		if end := endOfString(b, i); end >= 0 {
			return end + 1
		}
		return -1
	case '{', '[':
		depth := 0
		for ; i < len(b); i++ {
			switch b[i] {
			case '"':
				if i = endOfString(b, i); i < 0 {
					return -1
				}
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
		return -1
	}
	j := i
	for j < len(b) && !strings.ContainsRune(",}] \t\r\n", rune(b[j])) {
		j++
	}
	if j == i {
		return -1
	}
	return j
}
