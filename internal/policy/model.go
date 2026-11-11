package policy

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// ModelOf reads the top-level "model" of a JSON request body, the way every
// provider does. It reports false when there is no model kman can be sure of:
// the body isn't a JSON object or is cut short, the model isn't a string, or a
// parser that reads the body differently would pick another model: a repeated
// "model", a key that is "model" in other case (some parsers fold case), or
// invalid UTF-8. A model that can't be read can't be held to an allowlist, so
// the caller treats false as an unmetered request.
//
// It walks the body without copying it, and allocates only the model's own text.
func ModelOf(body []byte) (string, bool) {
	i := skipSpace(body, 0)
	if i >= len(body) || body[i] != '{' {
		return "", false
	}
	i++
	var value []byte // the model's string, with its quotes
	found, ambiguous := false, false
	for first := true; ; first = false {
		i = skipSpace(body, i)
		if i >= len(body) {
			return "", false
		}
		if body[i] == '}' {
			i++
			break
		}
		if !first {
			if body[i] != ',' {
				return "", false
			}
			i = skipSpace(body, i+1)
		}
		if i >= len(body) || body[i] != '"' {
			return "", false
		}
		end := scanString(body, i)
		if end < 0 {
			return "", false
		}
		kind := modelKey(body[i:end])
		i = skipSpace(body, end)
		if i >= len(body) || body[i] != ':' {
			return "", false
		}
		i = skipSpace(body, i+1)
		end = skipValue(body, i)
		if end < 0 {
			return "", false
		}
		switch kind {
		case keyVariant:
			ambiguous = true
		case keyModel:
			if found {
				ambiguous = true
			}
			found = true
			value = body[i:end]
		}
		i = end
	}
	if skipSpace(body, i) != len(body) || ambiguous || !found || len(value) == 0 || value[0] != '"' {
		return "", false
	}
	return decodeString(value)
}

const (
	keyOther = iota
	keyModel
	keyVariant // "model" in other case, or spelled with escapes in another way
)

// modelKey classifies a quoted key. A key with escapes is decoded first, since
// "model" is model to a parser.
func modelKey(quoted []byte) int {
	key := quoted[1 : len(quoted)-1]
	if bytes.IndexByte(key, '\\') >= 0 {
		var s string
		if json.Unmarshal(quoted, &s) != nil {
			return keyVariant
		}
		if s == "model" {
			return keyModel
		}
		if bytes.EqualFold([]byte(s), []byte("model")) {
			return keyVariant
		}
		return keyOther
	}
	if string(key) == "model" {
		return keyModel
	}
	if bytes.EqualFold(key, []byte("model")) {
		return keyVariant
	}
	return keyOther
}

func decodeString(quoted []byte) (string, bool) {
	inner := quoted[1 : len(quoted)-1]
	if bytes.IndexByte(inner, '\\') < 0 {
		if !utf8.Valid(inner) {
			return "", false
		}
		return string(inner), true
	}
	var s string
	if json.Unmarshal(quoted, &s) != nil || !utf8.ValidString(s) {
		return "", false
	}
	return s, true
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	return i
}

// scanString returns the index after the string that starts at b[i], or -1.
func scanString(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch c := b[i]; {
		case c == '"':
			return i + 1
		case c == '\\':
			i++ // the escaped byte; a bad escape is for the decoder
		case c < 0x20:
			return -1
		}
	}
	return -1
}

// maxDepth is how deep a value may nest. Providers' bodies are a few levels.
const maxDepth = 64

// skipValue returns the index after the JSON value that starts at b[i], or -1.
// It checks that brackets pair up and that strings end, which is all it needs.
func skipValue(b []byte, i int) int {
	if i >= len(b) {
		return -1
	}
	switch b[i] {
	case '"':
		return scanString(b, i)
	case '{', '[':
		var open [maxDepth]byte
		depth := 0
		for ; i < len(b); i++ {
			switch c := b[i]; c {
			case '"':
				if i = scanString(b, i); i < 0 {
					return -1
				}
				i--
			case '{', '[':
				if depth == maxDepth {
					return -1
				}
				open[depth] = c
				depth++
			case '}', ']':
				if depth == 0 || (c == '}') != (open[depth-1] == '{') {
					return -1
				}
				depth--
				if depth == 0 {
					return i + 1
				}
			}
		}
		return -1
	}
	// A number, true, false or null.
	start := i
	for i < len(b) {
		c := b[i]
		if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			break
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c == '-' || c == '+' || c == '.' || c == 'E') {
			return -1
		}
		i++
	}
	if i == start {
		return -1
	}
	return i
}
