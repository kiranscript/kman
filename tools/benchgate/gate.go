package main

import (
	"fmt"
	"io"
	"path"
	"slices"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

// Report is the gate's verdict. The gate fails when Failures is not empty.
// Notes are informational, such as a benchmark only one side has.
type Report struct {
	Failures []string
	Notes    []string
}

// series is every measurement of one benchmark in one unit.
type series map[string]map[string][]float64 // benchmark → unit → values

// Compare reads go test -bench output for the base and the head and judges
// the head. A benchmark fails when its median allocs/op rose at all, or when
// its sec/op rose by more than maxSlowdown (0.10 is 10%) and benchstat's
// Mann-Whitney U-test calls the change significant.
func Compare(base, head io.Reader, maxSlowdown float64) (Report, error) {
	b, err := read(base, "base")
	if err != nil {
		return Report{}, err
	}
	h, err := read(head, "head")
	if err != nil {
		return Report{}, err
	}
	var r Report
	for _, name := range sortedKeys(h) {
		old, ok := b[name]
		if !ok {
			r.Notes = append(r.Notes, name+": new benchmark, nothing to compare")
			continue
		}
		cur := h[name]
		if o, c := old["allocs/op"], cur["allocs/op"]; len(o) > 0 && len(c) > 0 {
			if mo, mc := median(o), median(c); mc > mo {
				r.Failures = append(r.Failures, fmt.Sprintf("%s: allocs/op rose from %g to %g", name, mo, mc))
			}
		}
		if o, c := old["sec/op"], cur["sec/op"]; len(o) > 0 && len(c) > 0 {
			so := benchmath.NewSample(o, &benchmath.DefaultThresholds)
			sc := benchmath.NewSample(c, &benchmath.DefaultThresholds)
			cmp := benchmath.AssumeNothing.Compare(so, sc)
			mo, mc := median(o), median(c)
			delta := (mc - mo) / mo
			if cmp.P < cmp.Alpha && delta > maxSlowdown {
				r.Failures = append(r.Failures, fmt.Sprintf("%s: sec/op %+.1f%%, over the %g%% limit", name, delta*100, maxSlowdown*100))
			}
		}
	}
	for _, name := range sortedKeys(b) {
		if _, ok := h[name]; !ok {
			r.Notes = append(r.Notes, name+": missing from the head")
		}
	}
	return r, nil
}

func read(r io.Reader, file string) (series, error) {
	s := series{}
	br := benchfmt.NewReader(r, file)
	for br.Scan() {
		res, ok := br.Result().(*benchfmt.Result)
		if !ok {
			continue // a syntax error: go test output carries other lines too
		}
		name := path.Base(res.GetConfig("pkg")) + " Benchmark" + string(res.Name.Full())
		if s[name] == nil {
			s[name] = map[string][]float64{}
		}
		for _, v := range res.Values {
			s[name][v.Unit] = append(s[name][v.Unit], v.Value)
		}
	}
	return s, br.Err()
}

func median(vs []float64) float64 {
	return benchmath.AssumeNothing.Summary(benchmath.NewSample(vs, &benchmath.DefaultThresholds), 0.95).Center
}

func sortedKeys(s series) []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
