package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is the version of the policy format this kman writes. The store
// keeps it beside each key's policy, so a later format can read an earlier one.
const Version = 1

// Policy is a key's policy. It is empty for now: later tickets add its fields,
// and a key's stored policy keeps working as they do.
type Policy struct{}

// Normalize checks a policy as JSON and returns its version and the form to
// store. Only a JSON object is a policy, and a field kman does not know is
// refused rather than kept, since a mistyped limit that silently limits nothing
// is worse than an error.
func Normalize(raw []byte) (version int, normalized []byte, err error) {
	if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '{' {
		return 0, nil, errors.New("a policy is a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Policy
	if err := dec.Decode(&p); err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return 0, nil, errors.New("the policy holds more than one JSON value")
	}
	normalized, err = json.Marshal(p)
	if err != nil {
		return 0, nil, fmt.Errorf("the policy: %w", err)
	}
	return Version, normalized, nil
}
