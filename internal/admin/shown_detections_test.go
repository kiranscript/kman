package admin

import (
	"slices"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/detect"
	"github.com/kirank55/kman/internal/store"
)

func TestRecordedFiringsIsDetectsBoundOnDetectionRows(t *testing.T) {
	if recordedFirings != detect.MaxDetectionsPerIncident {
		t.Fatalf("recordedFirings = %d, detect.MaxDetectionsPerIncident = %d", recordedFirings, detect.MaxDetectionsPerIncident)
	}
}

// Past the first recordedFirings rows every row is an escalation, whatever it
// looks like: detect records nothing else there.
func TestShownDetectionsTakeEveryRowPastTheFirstFiringsAsAnEscalation(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var ds []store.Detection
	for i := range recordedFirings + 3 {
		ds = append(ds, store.Detection{ID: int64(i + 1), At: t0.Add(time.Duration(i) * time.Second), Rule: "new_country", Severity: store.SeverityMedium})
	}
	var got []int64
	for _, d := range shownDetections(ds, 10) {
		got = append(got, d.ID)
	}
	// The first and the three past the firings, then 3 of the oldest and 3 of
	// the newest of the rest.
	want := []int64{1, 2, 3, 4, 98, 99, 100, 101, 102, 103}
	if !slices.Equal(got, want) {
		t.Errorf("shown %v, want %v", got, want)
	}
}

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
