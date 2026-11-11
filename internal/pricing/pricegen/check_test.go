package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirank55/kman/internal/pricing"
)

func entry(maxOut, context, input, output int64) pricing.Entry {
	return pricing.Entry{Mode: "chat", MaxOutput: maxOut, ContextWindow: context,
		Schedule: pricing.Schedule{Rates: pricing.Rates{Input: input, Output: output}}}
}

func data(global map[string]pricing.Entry, presets map[string]map[string]pricing.Entry) pricing.Data {
	return pricing.Data{Schema: pricing.Schema, GeneratedAt: now, Global: global, Presets: presets}
}

func kinds(fs []Flag) (out []string) {
	for _, f := range fs {
		out = append(out, f.Kind+" "+f.Preset+"/"+f.Model)
	}
	return out
}

func TestCheckFlagsMaxOutputEqualToTheContextWindow(t *testing.T) {
	head := data(map[string]pricing.Entry{
		"loose":   entry(1000, 1000, 1, 1),
		"fine":    entry(100, 1000, 1, 1),
		"nolimit": entry(100, 0, 1, 1),
	}, map[string]map[string]pricing.Entry{"xai": {"loose": entry(5, 5, 1, 1)}})
	flags := check(head, head, nil)
	got := strings.Join(kinds(flags), ",")
	if got != "max_output_equals_context /loose,max_output_equals_context xai/loose" {
		t.Errorf("flags = %s", got)
	}
	for _, f := range flags {
		if f.Fails {
			t.Errorf("%+v fails, but an unreviewed value only asks for a review", f)
		}
	}
}

func TestCheckFailsAReviewedMaxOutputEqualToTheContextWindowUnlessExplained(t *testing.T) {
	e := entry(1000, 1000, 1, 1)
	e.Reviewed = true
	head := data(map[string]pricing.Entry{"m": e}, nil)

	flags := check(head, head, map[key]reviewedEntry{{"openai", "m"}: {MaxOutput: 1000}})
	if len(flags) != 1 || !flags[0].Fails {
		t.Errorf("unexplained: %+v; want one failing flag", flags)
	}
	flags = check(head, head, map[key]reviewedEntry{{"openai", "m"}: {MaxOutput: 1000, EqualsContext: "no separate limit is published"}})
	if len(flags) != 1 || flags[0].Fails {
		t.Errorf("explained: %+v; want it flagged, not failing", flags)
	}
}

func TestCheckFlagsEveryValueThatChanged(t *testing.T) {
	long := entry(10, 0, 1, 1)
	long.Long = []pricing.Band{{Above: 100, Rates: pricing.Rates{Input: 2, Output: 2}}}
	longer := entry(10, 0, 1, 1)
	longer.Long = []pricing.Band{{Above: 100, Rates: pricing.Rates{Input: 3, Output: 2}}}
	base := data(map[string]pricing.Entry{
		"same": entry(10, 100, 1, 1), "maxout": entry(10, 100, 1, 1), "price": entry(10, 100, 1, 1),
		"gone": entry(10, 100, 1, 1), "tiered": long,
	}, nil)
	head := data(map[string]pricing.Entry{
		"same": entry(10, 100, 1, 1), "maxout": entry(20, 100, 1, 1), "price": entry(10, 100, 2, 1),
		"new": entry(10, 100, 1, 1), "tiered": longer,
	}, nil)
	flags := check(base, head, nil)
	got := map[string]Flag{}
	for _, f := range flags {
		got[f.Model+" "+f.Kind] = f
	}
	if len(flags) != 5 {
		t.Errorf("flags = %v", kinds(flags))
	}
	if f := got["maxout changed"]; f.Field != "max_output" || f.Old != "10" || f.New != "20" {
		t.Errorf("maxout: %+v", f)
	}
	if f := got["price changed"]; f.Field != "input" || f.Old != "1" || f.New != "2" {
		t.Errorf("price: %+v", f)
	}
	if f := got["tiered changed"]; f.Field != "long" {
		t.Errorf("tiered: %+v", f)
	}
	if _, ok := got["new added"]; !ok {
		t.Errorf("new: %v", kinds(flags))
	}
	if _, ok := got["gone removed"]; !ok {
		t.Errorf("gone: %v", kinds(flags))
	}
}

func TestTheCommittedTablePassesTheCheck(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"check", "--table", "../table.json", "--reviewed", "maxoutput.json"}, &out, &errOut, time.Now); code != 0 {
		t.Errorf("exit %d: %s", code, errOut.String())
	}
}

func TestEveryReviewedValueIsInTheCommittedTable(t *testing.T) {
	raw, err := os.ReadFile("maxoutput.json")
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := parseReviewed(raw)
	if err != nil {
		t.Fatal(err)
	}
	table, err := os.ReadFile("../table.json")
	if err != nil {
		t.Fatal(err)
	}
	tbl, err := pricing.Parse(table)
	if err != nil {
		t.Fatal(err)
	}
	for k, rev := range reviewed {
		e, ok := tbl.BuiltinEntry(k.preset, k.model)
		if !ok {
			t.Errorf("%s/%s is reviewed but not in the table; rerun scripts/update-price-data.sh", k.preset, k.model)
			continue
		}
		if e.MaxOutput != rev.MaxOutput || !e.Reviewed {
			t.Errorf("%s/%s: the table has max output %d (reviewed: %v), the reviewed file %d; rerun scripts/update-price-data.sh", k.preset, k.model, e.MaxOutput, e.Reviewed, rev.MaxOutput)
		}
	}
}

func TestCheckCommandPrintsAReportAndExitsOnFailure(t *testing.T) {
	dir := t.TempDir()
	reviewed := filepath.Join(dir, "reviewed.json")
	write := func(name string, d pricing.Data) string {
		t.Helper()
		raw, err := encode(d)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := os.WriteFile(reviewed, []byte(`{"openai": {"m": {"max_output": 10, "checked": "2026-11-03", "source": "https://example.test"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e := entry(10, 100, 1, 1)
	e.Reviewed = true
	base := write("base.json", data(map[string]pricing.Entry{"m": e}, nil))
	e2 := e
	e2.MaxOutput = 100
	bad := write("bad.json", data(map[string]pricing.Entry{"m": e2}, nil))

	var out, errOut bytes.Buffer
	code := run([]string{"check", "--base", base, "--table", bad, "--reviewed", reviewed}, &out, &errOut, time.Now)
	if code != 1 {
		t.Errorf("exit %d, want 1; stderr: %s", code, errOut.String())
	}
	for _, want := range []string{"max_output", "| 10 | 100 |", "equal"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"check", "--base", base, "--table", base, "--reviewed", reviewed}, &out, &errOut, time.Now); code != 0 {
		t.Errorf("no change: exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "No value changed") {
		t.Errorf("no change: %s", out.String())
	}
}

func TestGenerateCommandWritesTheTableAndLeavesItAloneWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	file := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	litellm := file("litellm.json", `{"gpt-x": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06, "max_output_tokens": 10}}`)
	genai := file("genai.json", `[{"id": "openai", "models": [{"id": "gpt-x", "prices": {"input_mtok": 1, "output_mtok": 2}}]}]`)
	dev := file("dev.json", `{"openai": {"models": {"gpt-x": {"cost": {"input": 3, "output": 2}, "limit": {"output": 10}}}}}`)
	reviewed := file("reviewed.json", `{}`)
	out := filepath.Join(dir, "table.json")
	rep := filepath.Join(dir, "report.md")
	args := func() []string {
		return []string{"generate", "--litellm", litellm, "--genai", genai, "--modelsdev", dev, "--reviewed", reviewed, "--out", out, "--report", rep}
	}
	var stdout, stderr bytes.Buffer
	if code := run(args(), &stdout, &stderr, func() time.Time { return now }); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	first, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if tbl, err := pricing.Parse(first); err != nil || !tbl.GeneratedAt().Equal(now) {
		t.Fatalf("table: %v, %v", tbl, err)
	}
	report, err := os.ReadFile(rep)
	if err != nil || !strings.Contains(string(report), "gpt-x") || !strings.Contains(string(report), "| 3000000 |") {
		t.Errorf("report %q, %v; want the disagreement on gpt-x's input", report, err)
	}

	// A week on, the sources say the same: generated_at and the hashes stay.
	later := func() time.Time { return now.AddDate(0, 0, 7) }
	file("litellm.json", `{"gpt-x": {"litellm_provider": "openai", "mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06, "max_output_tokens": 10, "note": "unrelated"}}`)
	if code := run(args(), &stdout, &stderr, later); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if second, _ := os.ReadFile(out); !bytes.Equal(first, second) {
		t.Errorf("the table changed though no price did:\n%s", second)
	}

	// A price moves: the table changes and is dated.
	file("dev.json", `{"openai": {"models": {"gpt-x": {"cost": {"input": 4, "output": 2}, "limit": {"output": 10}}}}}`)
	if code := run(args(), &stdout, &stderr, later); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	raw, _ := os.ReadFile(out)
	if tbl, err := pricing.Parse(raw); err != nil || !tbl.GeneratedAt().Equal(later()) {
		t.Errorf("table after a change: %v, %v", tbl.GeneratedAt(), err)
	}
}

func TestGenerateCommandRefusesAnEmptySourceAndLeavesTheTable(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "table.json")
	for _, name := range []string{"litellm", "genai", "dev", "reviewed"} {
		body := map[string]string{"litellm": `{}`, "genai": `[]`, "dev": `{}`, "reviewed": `{}`}[name]
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"generate", "--litellm", filepath.Join(dir, "litellm"), "--genai", filepath.Join(dir, "genai"),
		"--modelsdev", filepath.Join(dir, "dev"), "--reviewed", filepath.Join(dir, "reviewed"), "--out", out, "--report", filepath.Join(dir, "r.md")},
		&stdout, &stderr, func() time.Time { return now })
	if code != 1 || !strings.Contains(stderr.String(), "no rows") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("wrote a table: %v", err)
	}
}
