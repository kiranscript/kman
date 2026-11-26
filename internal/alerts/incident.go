package alerts

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/kiranscript/kman/internal/store"
)

// AlertIncident is the incident engine's producer (SPEC §9, §10): detect calls
// it in the transaction that opens or escalates an incident, so the alert and
// the incident commit together, and wakes the dispatcher after the commit. The
// alert is the incident's severity, names the key, the rules, what the
// detection saw and the commands that respond. change is "opened" or
// "escalated".
//
// The alert goes to the operator's own channels, so it names the client address
// the detection saw, as a canary's does (ADR 0013); the admin API is what holds
// addresses back. High and critical incidents take the priority lane.
func (s *Service) AlertIncident(tx *store.Tx, change string, inc store.Incident, det store.Detection) error {
	sev, err := ParseSeverity(string(inc.Severity))
	if err != nil {
		sev = Medium
	}
	title := "Incident opened"
	if change == "escalated" {
		title = "Incident escalated"
	}
	id := strconv.FormatInt(inc.ID, 10)
	a := Alert{
		Kind: "incident", Severity: sev, Priority: sev >= High, Title: title, KeyID: inc.KeyID, IncidentID: inc.ID,
		Rules: inc.Rules(), Time: det.At,
	}
	if err := nameKey(tx, &a); err != nil {
		return err
	}
	var ev store.Evidence
	if err := json.Unmarshal([]byte(det.EvidenceJSON), &ev); err == nil {
		if strings.HasPrefix(det.Rule, "new_") {
			a.Evidence = locationFacts(ev)
		} else {
			a.Evidence = evidenceFacts(ev)
		}
	}
	a.Evidence = append(a.Evidence, Fact{"Detections", strconv.FormatInt(inc.Detections, 10) + " so far"})
	a.Commands = []string{
		"kman incident show " + id,
		"kman incident ack " + id,
		"kman key disable " + inc.KeyID + " --incident " + id,
		"kman key rotate " + inc.KeyID + " --incident " + id,
		"kman incident this-was-me " + id,
	}
	return s.NotifyTx(tx, a)
}

// locationFacts lays out a location rule's detection (#89): the new place first,
// with its label, then the places the key normally uses, then where the request
// came from. The networks the key normally uses are addresses, which this alert
// may name as it names the address of the request: it goes to the operator's own
// channel.
func locationFacts(ev store.Evidence) []Fact {
	var out []Fact
	add := func(label, v string) {
		if v = clean(v); v != "" {
			out = append(out, Fact{label, v})
		}
	}
	add("New place", ev.Observed)
	// A list of the usual places is longer than a label.
	out = appendLong(out, "Usually used from", ev.Baseline)
	out = appendLong(out, "Usual networks", ev.Usual)
	rest := ev
	rest.Observed, rest.Baseline, rest.Usual = "", "", ""
	return append(out, evidenceFacts(rest)...)
}

func appendLong(out []Fact, label, v string) []Fact {
	if v = cleanTo(v, 300); v != "" {
		out = append(out, Fact{label, v})
	}
	return out
}

// evidenceFacts lays out what a detection saw, cleaned of what a database file
// or a header could carry into a message.
func evidenceFacts(ev store.Evidence) []Fact {
	var out []Fact
	add := func(label, v string) {
		if v = clean(v); v != "" {
			out = append(out, Fact{label, v})
		}
	}
	add("From", ev.Address)
	add("Network", ev.Network)
	add("Country", usableCountry(ev.Country))
	if ev.ASN != 0 {
		as := "AS" + strconv.FormatInt(ev.ASN, 10)
		if org := clean(ev.ASOrg); org != "" {
			as += " " + org
		}
		add("ASN", as)
	}
	add("Client", ev.Client)
	add("Observed", ev.Observed)
	add("Baseline", ev.Baseline)
	add("Detail", ev.Detail)
	return out
}
