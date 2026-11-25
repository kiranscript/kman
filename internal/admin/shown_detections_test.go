package admin

import (
	"testing"
	"time"

	"github.com/kirank55/kman/internal/store"
)

func TestShownDetectionsKeepEveryEscalationThenTheOldestAndNewest(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	row := func(id int64, at time.Time, rule string, sev store.Severity) store.Detection {
		return store.Detection{ID: id, At: at, Rule: rule, Severity: sev}
	}
	ids := func(ds []store.Detection) []int64 {
		var out []int64
		for _, d := range ds {
			out = append(out, d.ID)
		}
		return out
	}
	var ds []store.Detection
	for i := range 10 {
		ds = append(ds, row(int64(i+1), t0.Add(time.Duration(i)*time.Minute), "new_country", store.SeverityMedium))
	}
	if got := shownDetections(ds, 10); len(got) != 10 {
		t.Fatalf("10 of 10 shown as %v", ids(got))
	}
	// 11: a firing after an hour of quiet, 12: a new rule, 13: a higher severity.
	ds = append(ds,
		row(11, t0.Add(2*time.Hour), "new_country", store.SeverityMedium),
		row(12, t0.Add(2*time.Hour+time.Minute), "new_asn", store.SeverityMedium),
		row(13, t0.Add(2*time.Hour+2*time.Minute), "new_asn", store.SeverityHigh),
		row(14, t0.Add(2*time.Hour+3*time.Minute), "new_asn", store.SeverityHigh))
	got := ids(shownDetections(ds, 6))
	want := []int64{1, 2, 11, 12, 13, 14} // the escalations 1, 11, 12, 13, then the oldest and the newest of the rest
	if len(got) != len(want) {
		t.Fatalf("shown %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shown %v, want %v", got, want)
		}
	}
	// With room for fewer than the escalations: the first, then the newest.
	got = ids(shownDetections(ds, 3))
	if len(got) != 3 || got[0] != 1 || got[1] != 12 || got[2] != 13 {
		t.Errorf("shown %v, want [1 12 13]", got)
	}
}
