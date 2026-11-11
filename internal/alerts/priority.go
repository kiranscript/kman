package alerts

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/kirank55/kman/internal/events"
	"github.com/kirank55/kman/internal/store"
)

// The error codes of a refusal that takes the priority lane, as gateway sets
// them on the event.
const (
	codeKeyDisabled = "key_disabled"
)

// RunPriority turns the events of the priority lane into alerts, until the
// subscription closes: the use of a disabled key is high, and the use of a
// canary critical. The lane goes straight to the outbox without waiting on the
// event store (SPEC §9). Subscribe with events.SubOptions{PriorityOnly: true}.
//
// A key in use is a stream of events, so one key alerts once per
// ThrottleWindow, and the next alert says how many uses it held back.
func (s *Service) RunPriority(sub *events.Subscription) {
	held := map[string]*throttled{}
	for e := range sub.C {
		// A canary answers invalid_key like any bad key, so the gateway marks the
		// event; the code alone never makes one.
		if e.KeyID == "" || !(e.Canary || e.BlockedCode == codeKeyDisabled) {
			continue
		}
		now := s.o.Now()
		canary := e.Canary
		id := e.BlockedCode + "/" + e.KeyID
		t := held[id]
		if t != nil && now.Sub(t.at) < s.o.ThrottleWindow {
			t.suppressed++
			continue
		}
		suppressed := 0
		if t != nil {
			suppressed = t.suppressed
		}
		if len(held) >= maxThrottled {
			for k, v := range held { // bounded: an entry past its window forgets nothing that matters
				if now.Sub(v.at) >= s.o.ThrottleWindow {
					delete(held, k)
				}
			}
		}
		held[id] = &throttled{at: now}
		a := usedKeyAlert(e, canary, suppressed)
		err := s.o.Store.Do(context.Background(), func(tx *store.Tx) error {
			if err := nameKey(tx, &a); err != nil {
				return err
			}
			return s.NotifyTx(tx, a)
		})
		if err == nil {
			s.Wake()
		} else {
			s.o.Log.Error("alerts: could not queue an alert for a priority event", "key", e.KeyID, "err", err)
			delete(held, id) // so the next use tries again
		}
	}
}

// maxThrottled bounds the keys RunPriority remembers.
const maxThrottled = 4096

type throttled struct {
	at         time.Time
	suppressed int
}

// usedKeyAlert is the alert for the use of a disabled key or a canary. It holds
// where the request came from and its method, never anything the request said:
// the path is the caller's to write, so it stays out of what goes to a channel.
func usedKeyAlert(e events.Event, canary bool, held int) Alert {
	a := Alert{Kind: "disabled_key", Severity: High, Priority: true, Title: "A disabled key was used", KeyID: e.KeyID, Time: e.Time}
	if canary {
		a.Kind, a.Severity, a.Title = "canary", Critical, "A canary key was used"
	}
	if e.ClientIP.IsValid() {
		a.Evidence = append(a.Evidence, Fact{"From", e.ClientIP.String()})
	}
	if knownMethods[e.Method] { // a method is a token the caller picks, so only the standard ones are named
		a.Evidence = append(a.Evidence, Fact{"Method", e.Method})
	}
	if held > 0 {
		a.Evidence = append(a.Evidence, Fact{"Held back", strconv.Itoa(held) + " more uses since the last alert"})
	}
	a.Commands = []string{"kman key show " + e.KeyID}
	return a
}

var knownMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true,
}

// nameKey fills in the name of the key an alert is about, when it has one.
func nameKey(tx *store.Tx, a *Alert) error {
	k, err := tx.Key(a.KeyID)
	switch {
	case err == nil:
		a.KeyName = k.Name
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	return nil
}
