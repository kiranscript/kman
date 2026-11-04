package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// bench renders go test -bench output for one package, one line per run,
// with ns/op from nsPerOp and the given allocs/op.
func bench(name string, allocs int, nsPerOp ...float64) string {
	var b strings.Builder
	b.WriteString("goos: linux\ngoarch: amd64\npkg: github.com/kirank55/kman/internal/gateway\n")
	for _, ns := range nsPerOp {
		fmt.Fprintf(&b, "%s-8\t1000000\t%.0f ns/op\t64 B/op\t%d allocs/op\n", name, ns, allocs)
	}
	return b.String()
}

func compare(t *testing.T, base, head string) Report {
	t.Helper()
	r, err := Compare(strings.NewReader(base), strings.NewReader(head), 0.10)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var steady = []float64{1000, 1002, 998, 1001, 999, 1003, 997, 1000}

func scaled(f float64) []float64 {
	var out []float64
	for _, v := range steady {
		out = append(out, v*f)
	}
	return out
}

func TestSlowdownOverTheLimitFails(t *testing.T) {
	r := compare(t, bench("BenchmarkGateway", 3, steady...), bench("BenchmarkGateway", 3, scaled(1.15)...))
	want := []string{"gateway BenchmarkGateway-8: sec/op +15.0%, over the 10% limit"}
	if !slices.Equal(r.Failures, want) {
		t.Errorf("failures = %q, want %q", r.Failures, want)
	}
}

func TestSlowdownUnderTheLimitPasses(t *testing.T) {
	r := compare(t, bench("BenchmarkGateway", 3, steady...), bench("BenchmarkGateway", 3, scaled(1.05)...))
	if len(r.Failures) != 0 {
		t.Errorf("failures = %q, want none", r.Failures)
	}
}

func TestAnyRiseInAllocationsFails(t *testing.T) {
	r := compare(t, bench("BenchmarkGateway", 3, steady...), bench("BenchmarkGateway", 4, steady...))
	want := []string{"gateway BenchmarkGateway-8: allocs/op rose from 3 to 4"}
	if !slices.Equal(r.Failures, want) {
		t.Errorf("failures = %q, want %q", r.Failures, want)
	}
}

func TestSlowdownThatIsOnlyNoisePasses(t *testing.T) {
	// The medians differ by 20%, but the samples overlap, so the U-test
	// cannot call it a change.
	base := bench("BenchmarkGateway", 3, 1000, 1400, 900, 1300)
	head := bench("BenchmarkGateway", 3, 1200, 1350, 950, 1500)
	r := compare(t, base, head)
	if len(r.Failures) != 0 {
		t.Errorf("failures = %q, want none", r.Failures)
	}
}

func TestBenchmarksOnOneSideAreNotesNotFailures(t *testing.T) {
	r := compare(t, bench("BenchmarkOld", 3, steady...), bench("BenchmarkNew", 9, scaled(3)...))
	if len(r.Failures) != 0 {
		t.Errorf("failures = %q, want none", r.Failures)
	}
	want := []string{
		"gateway BenchmarkNew-8: new benchmark, nothing to compare",
		"gateway BenchmarkOld-8: missing from the head",
	}
	if !slices.Equal(r.Notes, want) {
		t.Errorf("notes = %q, want %q", r.Notes, want)
	}
}

func TestNoBenchmarksPasses(t *testing.T) {
	r := compare(t, "PASS\nok  \tgithub.com/kirank55/kman/internal/token\t0.01s\n", "")
	if len(r.Failures) != 0 || len(r.Notes) != 0 {
		t.Errorf("report = %+v, want it empty", r)
	}
}
