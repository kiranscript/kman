package alerts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// DefaultCredentialWarningWindow is how often one upstream's refused credential
// warns: a refused credential is refused by every request.
const DefaultCredentialWarningWindow = time.Hour

// RunCredentialWarnings turns events into credential warnings, until the
// subscription closes. A provider that answers 401 to a request kman forwarded
// has refused the credential kman presented for the key: it was revoked or has
// expired there, and every key on that upstream is failing (SPEC §9). The
// warning names the upstream and its credentials, never a secret. Subscribe with
// ordinary events.SubOptions.
//
// An upstream alerts once per CredentialWarningWindow, and the next alert says
// how many refusals it held back.
func (s *Service) RunCredentialWarnings(sub *events.Subscription) {
	held := map[string]*throttled{}
	for e := range sub.C {
		if e.Kind != events.KindRequest || e.Status != http.StatusUnauthorized || e.UpstreamID == "" || e.BlockedCode != "" {
			continue
		}
		now := s.o.Now()
		t := held[e.UpstreamID]
		if t != nil && now.Sub(t.at) < s.o.CredentialWarningWindow {
			t.suppressed++
			continue
		}
		suppressed := 0
		if t != nil {
			suppressed = t.suppressed
		}
		if len(held) >= maxThrottled {
			for k, v := range held {
				if now.Sub(v.at) >= s.o.CredentialWarningWindow {
					delete(held, k)
				}
			}
		}
		held[e.UpstreamID] = &throttled{at: now}
		queued, err := s.warnCredential(e, now, suppressed)
		switch {
		case err != nil:
			s.o.Log.Error("alerts: could not queue a credential warning", "upstream", e.UpstreamID, "err", err)
			delete(held, e.UpstreamID) // so the next refusal tries again
		case !queued:
			delete(held, e.UpstreamID) // an upstream kman no longer holds: nothing to warn about
		default:
			s.Wake()
		}
	}
}

func (s *Service) warnCredential(e events.Event, now time.Time, held int) (queued bool, err error) {
	err = s.o.Store.Do(context.Background(), func(tx *store.Tx) error {
		up, err := tx.Upstream(e.UpstreamID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		creds, err := tx.Credentials()
		if err != nil {
			return err
		}
		var names []string
		var first string
		for _, c := range creds {
			if c.UpstreamID == up.ID {
				names = append(names, c.Name)
				if first == "" {
					first = c.Name
				}
			}
		}
		a := Alert{
			Kind: "credential_refused", Severity: High, Warning: true, Time: now,
			Title: fmt.Sprintf("%s refused kman's credential", up.Name),
			Evidence: []Fact{
				{"Upstream", up.Name},
				{"Credential", strings.Join(names, ", ")},
				{"Answered", "401 Unauthorized"},
			},
			Commands: []string{"kman upstream show " + up.Name},
		}
		if e.KeyID != "" {
			a.Evidence = append(a.Evidence, Fact{"Request by key", e.KeyID})
		}
		if held > 0 {
			a.Evidence = append(a.Evidence, Fact{"Held back", strconv.Itoa(held) + " more refusals since the last alert"})
		}
		if first != "" {
			a.Commands = append(a.Commands, "kman credential update "+first+" --secret")
		}
		queued = true
		return s.NotifyTx(tx, a)
	})
	return queued, err
}
