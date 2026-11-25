package alerts

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

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
// ThrottleWindow, and the next alert says how many uses it held back. A canary
// differs in one way: the first use after it was armed always alerts, window or
// not, since a reset is the operator saying "tell me again". The canary's own
// row says whether that is this use, so it survives a restart, and a use from
// an address on its ignore list is no use at all (#87).
//
// What the lane holds when it is read is handled together, in one transaction
// (at most priorityBatch events), so a client hammering a canary costs the
// store, which the event writer shares, one transaction per batch and not one
// per use; a use alerts no later than it did alone.
func (s *Service) RunPriority(sub *events.Subscription) {
	held := map[string]*throttled{}
	for e := range sub.C {
		batch := []events.Event{e}
	drain:
		for len(batch) < priorityBatch {
			select {
			case e, ok := <-sub.C:
				if !ok {
					break drain
				}
				batch = append(batch, e)
			default:
				break drain
			}
		}
		s.priorityBatch(held, batch)
	}
}

// priorityBatch bounds the events of the priority lane handled in one
// transaction.
const priorityBatch = 256

// priorityBatch handles events of the priority lane. A canary's use is recorded
// on its row, and the alert is queued, in one transaction for the batch, so
// that the state and the alerts are never apart. If it fails, each event is
// tried alone, so that one bad event costs no other its alert.
func (s *Service) priorityBatch(held map[string]*throttled, batch []events.Event) {
	now := s.o.Now()
	var work []events.Event
	for _, e := range batch {
		// A canary answers invalid_key like any bad key, so the gateway marks the
		// event; the code alone never makes one.
		if e.KeyID == "" || !(e.Canary || e.BlockedCode == codeKeyDisabled) {
			continue
		}
		if t := held[throttleID(e)]; t != nil && now.Sub(t.at) < s.o.ThrottleWindow && !e.Canary {
			// A disabled key's use inside its window only adds to the count. It
			// must not wait for the store, which the event writer shares: a client
			// that hammers a disabled key would fill this subscription and drop a
			// canary's first use behind it.
			t.suppressed++
			continue
		}
		work = append(work, e)
	}
	if len(work) == 0 {
		return
	}
	var pending map[string]*throttled
	var queued bool
	s.priorityTxs.Add(1)
	err := s.o.Store.Do(context.Background(), func(tx *store.Tx) error {
		pending, queued = map[string]*throttled{}, false
		for _, e := range work {
			q, err := s.priorityUse(tx, held, pending, now, e)
			if err != nil {
				return err
			}
			queued = queued || q
		}
		return nil
	})
	if err != nil {
		if len(work) > 1 {
			s.o.Log.Warn("alerts: a batch of priority events failed; handling each alone", "events", len(work), "err", err)
			for _, e := range work {
				s.priorityBatch(held, []events.Event{e})
			}
			return
		}
		s.o.Log.Error("alerts: could not queue an alert for a priority event", "key", work[0].KeyID, "err", err)
		return
	}
	for id, t := range pending {
		if _, ok := held[id]; !ok && len(held) >= maxThrottled {
			for k, v := range held { // bounded: an entry past its window forgets nothing that matters
				if now.Sub(v.at) >= s.o.ThrottleWindow {
					delete(held, k)
				}
			}
		}
		held[id] = t
	}
	if queued {
		s.Wake()
	}
}

// throttleID is what a priority event's alerts are throttled by: its key, and
// whether it is a canary's use or a disabled key's.
func throttleID(e events.Event) string {
	if e.Canary {
		return "canary/" + e.KeyID
	}
	return e.BlockedCode + "/" + e.KeyID
}

// priorityUse handles one event of a batch in its transaction, and says whether
// it queued an alert. What it does to the throttle goes to pending, which is
// read before held, so a later event of the batch sees it, and which the batch
// keeps only once the transaction has committed.
func (s *Service) priorityUse(tx *store.Tx, held, pending map[string]*throttled, now time.Time, e events.Event) (bool, error) {
	id := throttleID(e)
	t, ok := pending[id]
	if !ok {
		t = held[id]
	}
	inWindow := t != nil && now.Sub(t.at) < s.o.ThrottleWindow
	var canary *store.Canary
	var incident int64
	firedNow, news := false, false
	if e.Canary {
		c, err := tx.Canary(e.KeyID)
		switch {
		case err == nil:
			if c.Ignored(e.ClientIP) {
				return false, nil
			}
			firedNow, c, err = tx.RecordCanaryUse(e.KeyID, e.Time, store.CanaryUse{
				Address: e.ClientIP, Country: usableCountry(e.Country), ASN: e.ASN, ASOrg: clean(e.ASOrg), Client: clean(e.Client),
			})
			if err != nil {
				return false, err
			}
			canary = &c
			if s.o.CanaryIncident != nil {
				// The use is a detection: it opens or escalates the canary's
				// incident in this transaction, so the incident, the canary's
				// row and the alert are never apart. The alert below is still
				// the canary's own, with its own throttle (ADR 0013).
				if incident, news, err = s.o.CanaryIncident(tx, e.KeyID, e.Time, store.Evidence{
					Address: addressOf(e), Country: usableCountry(e.Country), ASN: e.ASN, ASOrg: clean(e.ASOrg), Client: clean(e.Client),
					Detail: plantedDetail(c),
				}); err != nil {
					return false, err
				}
			}
		case !errors.Is(err, store.ErrNotFound):
			return false, err
		}
	}
	// Inside the window only a use that armed the canary's first firing, or
	// that opened or escalated its incident (a use after the last one was
	// resolved), alerts: the rest are counted for the next alert.
	if inWindow && !firedNow && !news {
		pending[id] = &throttled{at: t.at, suppressed: t.suppressed + 1}
		return false, nil
	}
	suppressed := 0
	if t != nil {
		suppressed = t.suppressed
	}
	a := usedKeyAlert(e, canary, suppressed, incident)
	if err := nameKey(tx, &a); err != nil {
		return false, err
	}
	if err := s.NotifyTx(tx, a); err != nil {
		return false, err
	}
	pending[id] = &throttled{at: now}
	return true, nil
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
// canary is the canary's row when the use was of one that has a row.
func usedKeyAlert(e events.Event, canary *store.Canary, held int, incident int64) Alert {
	a := Alert{Kind: "disabled_key", Severity: High, Priority: true, Title: "A disabled key was used", KeyID: e.KeyID, Time: e.Time}
	if e.Canary {
		a.Kind, a.Severity, a.Title = "canary", Critical, "A canary key was used"
	}
	if canary != nil && canary.Planted != "" {
		a.Evidence = append(a.Evidence, Fact{"Planted", cleanTo(canary.Planted, maxPlanted)})
	}
	if e.ClientIP.IsValid() {
		a.Evidence = append(a.Evidence, Fact{"From", e.ClientIP.String()})
	}
	if c := usableCountry(e.Country); c != "" {
		a.Evidence = append(a.Evidence, Fact{"Country", c})
	}
	if e.ASN != 0 {
		as := "AS" + strconv.FormatInt(e.ASN, 10)
		if org := clean(e.ASOrg); org != "" {
			as += " " + org
		}
		a.Evidence = append(a.Evidence, Fact{"ASN", as})
	}
	if c := clean(e.Client); c != "" {
		a.Evidence = append(a.Evidence, Fact{"Client", c})
	}
	if knownMethods[e.Method] { // a method is a token the caller picks, so only the standard ones are named
		a.Evidence = append(a.Evidence, Fact{"Method", e.Method})
	}
	if held > 0 {
		a.Evidence = append(a.Evidence, Fact{"Held back", strconv.Itoa(held) + " more uses since the last alert"})
	}
	a.Commands = []string{"kman key show " + e.KeyID}
	if e.Canary {
		a.Commands = []string{"kman key places " + e.KeyID, "kman canary list", "kman canary reset " + e.KeyID}
	}
	if incident != 0 {
		a.IncidentID = incident
		a.Commands = append([]string{"kman incident show " + strconv.FormatInt(incident, 10)}, a.Commands...)
	}
	return a
}

// addressOf is the client address of an event as text, "" for none.
func addressOf(e events.Event) string {
	if !e.ClientIP.IsValid() {
		return ""
	}
	return e.ClientIP.Unmap().String()
}

// plantedDetail is what a canary's incident says of where it was planted.
func plantedDetail(c store.Canary) string {
	if c.Planted == "" {
		return ""
	}
	return "Planted: " + cleanTo(c.Planted, maxPlanted)
}

// maxLabel bounds a label that came from a file or a header, and maxPlanted what
// the operator wrote of where a canary is planted (admin holds it to the same).
const (
	maxLabel   = 64
	maxPlanted = 200
)

// clean is s as one short line of printable text, for a value that came from a
// database file or a header and goes into an alert.
func clean(s string) string { return cleanTo(s, maxLabel) }

// cleanTo is clean with a limit of n characters.
func cleanTo(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// usableCountry is c if it is an ISO 3166-1 alpha-2 code, as GeoIP gives, and
// empty otherwise.
func usableCountry(c string) string {
	if len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' {
		return c
	}
	return ""
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
