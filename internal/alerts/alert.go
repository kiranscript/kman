package alerts

import (
	"fmt"
	"strings"
	"time"
)

// Severity says how much an alert matters. A channel takes alerts at or above
// its minimum.
type Severity int

// The severities, lowest first (SPEC §10). Info is not one of them: an alert is
// a message someone should read.
const (
	Low Severity = iota + 1
	Medium
	High
	Critical
)

var severityNames = map[Severity]string{Low: "low", Medium: "medium", High: "high", Critical: "critical"}

func (s Severity) String() string {
	if n, ok := severityNames[s]; ok {
		return n
	}
	return fmt.Sprintf("severity(%d)", int(s))
}

// ParseSeverity reads a severity by name, as a channel's min_severity holds it.
func ParseSeverity(name string) (Severity, error) {
	for s, n := range severityNames {
		if n == strings.ToLower(strings.TrimSpace(name)) {
			return s, nil
		}
	}
	return 0, fmt.Errorf("unknown severity %q: use low, medium, high or critical", name)
}

// Severities are the names a channel's min_severity takes, lowest first.
func Severities() []string { return []string{"low", "medium", "high", "critical"} }

// MarshalText and UnmarshalText keep a severity readable in the outbox.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

func (s *Severity) UnmarshalText(b []byte) error {
	v, err := ParseSeverity(string(b))
	*s = v
	return err
}

// Fact is one line of evidence: what was seen.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Alert is what kman tells an operator. It is built from fields that cannot
// carry a secret or request content: producers fill in names, ids, counts and
// addresses, and nothing of what an app sent or received.
type Alert struct {
	// Kind is what raised it, such as "audit", "disabled_key" or "canary".
	Kind     string   `json:"kind"`
	Severity Severity `json:"severity"`
	// Warning marks a spend or lifecycle warning, which a channel can turn off.
	Warning bool `json:"warning,omitempty"`
	// Priority alerts are delivered before the rest of a channel's backlog.
	Priority bool   `json:"priority,omitempty"`
	Title    string `json:"title"`
	// KeyID and KeyName say which virtual key it is about, when one is.
	KeyID      string    `json:"key_id,omitempty"`
	KeyName    string    `json:"key_name,omitempty"`
	IncidentID int64     `json:"incident_id,omitempty"`
	Rules      []string  `json:"rules,omitempty"`
	Evidence   []Fact    `json:"evidence,omitempty"`
	Commands   []string  `json:"commands,omitempty"` // copy-paste kman commands
	Time       time.Time `json:"time"`
}
