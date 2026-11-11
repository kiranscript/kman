package policy

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var method = regexp.MustCompile(`^(\*|[A-Z]+)$`)

// CheckPathRule reports whether rule is a path rule: "METHOD /path", with the
// method in capitals or * for any. In the path, a segment of * is any one
// segment, and a last segment of ** is the rest of the path, one segment or
// more. It is the one place the grammar is written: an upstream's list and a
// key's list are checked and matched by it.
func CheckPathRule(rule string) error {
	_, err := parseRule(rule)
	return err
}

// segment kinds of a rule.
const (
	segLiteral = iota
	segAny     // *
	segRest    // **
)

type segment struct {
	kind    int
	literal string
}

type pathRule struct {
	method   string // "" for any method
	segments []segment
}

func parseRule(rule string) (pathRule, error) {
	m, path, ok := strings.Cut(rule, " ")
	if !ok || !method.MatchString(m) || !strings.HasPrefix(path, "/") {
		return pathRule{}, errors.New("use the form \"METHOD /path\", with the method in capitals or *")
	}
	if strings.ContainsFunc(path, func(r rune) bool {
		return r < 0x20 || r == 0x7f || r == ' ' || r == '\\' || r == '?' || r == '#' || r == '%'
	}) {
		return pathRule{}, errors.New("the path may not hold spaces, control characters, a backslash, a query, a fragment or a % escape")
	}
	r := pathRule{}
	if m != "*" {
		r.method = m
	}
	parts := strings.Split(path[1:], "/")
	for i, seg := range parts {
		switch {
		case seg == "":
			return pathRule{}, errors.New("the path may not hold an empty segment")
		case seg == "." || seg == "..":
			return pathRule{}, errors.New("the path may not hold . or .. segments")
		case seg == "**" && i != len(parts)-1:
			return pathRule{}, errors.New("** must be the last segment")
		case seg == "**":
			r.segments = append(r.segments, segment{kind: segRest})
		case seg == "*":
			r.segments = append(r.segments, segment{kind: segAny})
		case strings.Contains(seg, "*"):
			return pathRule{}, errors.New("a wildcard must be a whole segment")
		default:
			r.segments = append(r.segments, segment{literal: seg})
		}
	}
	return r, nil
}

// Paths is a compiled list of path rules. Build one with CompilePaths, once,
// not per request.
type Paths struct {
	rules []pathRule
}

// CompilePaths compiles rules, each checked by CheckPathRule.
func CompilePaths(rules []string) (*Paths, error) {
	p := &Paths{rules: make([]pathRule, 0, len(rules))}
	for _, rule := range rules {
		r, err := parseRule(rule)
		if err != nil {
			return nil, fmt.Errorf("path rule %q: %w", rule, err)
		}
		p.rules = append(p.rules, r)
	}
	return p, nil
}

// maxSegments is how many segments a path may hold on the allocation-free
// path. A longer one is still matched.
const maxSegments = 16

// Match reports whether a request with this method and escaped path (what
// net/url calls EscapedPath, as the app sent it) is allowed by the list. A path
// that an upstream might read differently from kman matches nothing: one with
// an empty segment (other than a last one), a . or .. segment, or a segment that
// decodes to hold a slash, a backslash or a control character. Segments are
// compared decoded, so %63hat is chat.
func (p *Paths) Match(method, escapedPath string) bool {
	if p == nil {
		return false
	}
	var buf [maxSegments]string
	segs, ok := splitPath(buf[:0], escapedPath)
	if !ok {
		return false
	}
	for i := range p.rules {
		r := &p.rules[i]
		if (r.method == "" || r.method == method) && r.matches(segs) {
			return true
		}
	}
	return false
}

func (r *pathRule) matches(segs []string) bool {
	for i, s := range r.segments {
		switch s.kind {
		case segRest:
			return i < len(segs)
		case segAny:
			if i >= len(segs) {
				return false
			}
		default:
			if i >= len(segs) || segs[i] != s.literal {
				return false
			}
		}
	}
	return len(segs) == len(r.segments)
}

// splitPath appends the decoded segments of an escaped path to dst. It reports
// false for a path no rule may match.
func splitPath(dst []string, path string) ([]string, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	rest := path[1:]
	for {
		seg, more, hasMore := strings.Cut(rest, "/")
		if seg == "" && hasMore {
			return nil, false // an empty segment that isn't the last
		}
		if strings.IndexByte(seg, '%') >= 0 {
			decoded, err := url.PathUnescape(seg)
			if err != nil {
				return nil, false
			}
			seg = decoded
		}
		if seg == "." || seg == ".." || strings.ContainsFunc(seg, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '/' || r == '\\' }) {
			return nil, false
		}
		dst = append(dst, seg)
		if !hasMore {
			return dst, true
		}
		rest = more
	}
}
