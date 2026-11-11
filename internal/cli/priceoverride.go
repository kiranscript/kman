package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kirank55/kman/internal/admin"
)

// parseMillionths reads a decimal with no more than six places, such as 2.50,
// as an exact count of millionths. Prices and multipliers never pass through a
// float.
func parseMillionths(s string) (int64, error) {
	whole, frac, dotted := strings.Cut(s, ".")
	allDigits := func(t string) bool {
		return t != "" && strings.Trim(t, "0123456789") == ""
	}
	if !allDigits(whole) || (dotted && !allDigits(frac)) {
		return 0, fmt.Errorf("%q is not a number such as 2.50", s)
	}
	if len(frac) > 6 {
		return 0, fmt.Errorf("%q has more than six decimal places", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > math.MaxInt64/1_000_000-1 {
		return 0, fmt.Errorf("%q is too large", s)
	}
	f := int64(0)
	if frac != "" {
		// At most six digits, so this can't fail.
		f, _ = strconv.ParseInt((frac + "000000")[:6], 10, 64)
	}
	return w*1_000_000 + f, nil
}

// decimal prints millionths with no more digits than it takes: 1.1, 1, 0.25.
func decimal(n int64) string {
	whole, frac := n/1_000_000, n%1_000_000
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return fmt.Sprintf("%d.%s", whole, strings.TrimRight(fmt.Sprintf("%06d", frac), "0"))
}

// priceFlag is a flag holding dollars per million tokens, which may start with
// the $ that kman price list prints. It is nil until given.
type priceFlag struct {
	micro **int64
}

func (p priceFlag) String() string { return "" }

func (p priceFlag) Set(v string) error {
	n, err := parseMillionths(strings.TrimPrefix(v, "$"))
	if err != nil {
		return err
	}
	*p.micro = &n
	return nil
}

func priceSet(ctx context.Context, c *call) error {
	fs := c.flags("price set <model> --input dollars --output dollars --max-output tokens [--upstream name] [optional prices] [--json]\n\n"+
		"Prices are dollars per million tokens. Without --upstream it applies to the whole install. It replaces the model's whole\n"+
		"built-in entry, its long-context bands, service tiers and per-call fees included, until kman price rm removes it.", true)
	var req admin.PriceOverrideSet
	fs.StringVar(&req.Upstream, "upstream", "", "price it on this upstream only, where the upstream's price multiplier doesn't apply")
	fs.Var(priceFlag{&req.InputMicro}, "input", "the input price (required)")
	fs.Var(priceFlag{&req.OutputMicro}, "output", "the output price (required)")
	maxOutput := fs.Int64("max-output", 0, "the most tokens one response can output, which sizes reservations (required)")
	fs.Var(priceFlag{&req.CachedMicro}, "cached", "the cached input price (default: the input price)")
	fs.Var(priceFlag{&req.CacheWriteMicro}, "cache-write", "the cache write price (default: the input price)")
	fs.Var(priceFlag{&req.CacheWrite1hMicro}, "cache-write-1h", "the one-hour cache write price (default: the cache write price)")
	fs.Var(priceFlag{&req.AudioInputMicro}, "audio-input", "the audio input price (default: the input price)")
	fs.Var(priceFlag{&req.AudioOutputMicro}, "audio-output", "the audio output price (default: the output price)")
	fs.Var(priceFlag{&req.AudioCachedMicro}, "audio-cached", "the cached audio input price (default: the audio input price)")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	req.Model = pos[0]
	if err := checkUpstreamFlag(fs); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-output" {
			req.MaxOutput = maxOutput
		}
	})
	if req.InputMicro == nil || req.OutputMicro == nil || req.MaxOutput == nil {
		return errors.New("an override needs all of --input, --output and --max-output")
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.SetPriceOverride(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "%s now costs %s in and %s out per million tokens, max output %d, %s.\n",
		got.Model, dollars(got.InputMicro), dollars(got.OutputMicro), got.MaxOutput, overrideReach(got))
	if len(got.Hides) > 0 {
		fmt.Fprintf(c.env.Stdout, "This hides the built-in entry in %s until you remove it with kman price rm.\n", strings.Join(got.Hides, ", "))
	}
	return nil
}

// checkUpstreamFlag refuses an empty --upstream, as from an unset shell
// variable, which would otherwise mean the whole install.
func checkUpstreamFlag(fs *flag.FlagSet) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "upstream" && f.Value.String() == "" {
			err = errors.New("--upstream is empty: leave it out to mean the whole install")
		}
	})
	return err
}

// overrideReach says where an override applies.
func overrideReach(o admin.PriceOverride) string {
	if o.Upstream != "" {
		return "on upstream " + o.Upstream
	}
	return "across the install"
}

func priceRm(ctx context.Context, c *call) error {
	fs := c.flags("price rm <model> [--upstream name] [--json]", true)
	upstream := fs.String("upstream", "", "remove the override on this upstream, not the install-wide one")
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	if err := checkUpstreamFlag(fs); err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.RemovePriceOverride(ctx, admin.PriceOverrideRef{Upstream: *upstream, Model: pos[0]})
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Removed the price override of %s %s.\n", got.Model, overrideReach(got))
	return nil
}

// importEntry is one override in a file. What kman price list --json adds to an
// override is accepted and ignored, so its output can be imported as it is, and
// anything else is refused: a misspelt optional price would otherwise bill at
// the price it falls back to.
type importEntry struct {
	admin.PriceOverrideSet
	UpstreamID json.RawMessage `json:"upstream_id"`
	Hides      json.RawMessage `json:"hides"`
	Actor      json.RawMessage `json:"actor"`
	UpdatedAt  json.RawMessage `json:"updated_at"`
}

// importFile is a file of overrides, or the output of kman price list --json.
type importFile struct {
	Overrides []importEntry   `json:"overrides"`
	Status    json.RawMessage `json:"status"`
	Models    json.RawMessage `json:"models"`
}

func parseImport(raw []byte) (admin.PriceOverrideImport, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f importFile
	if err := dec.Decode(&f); err != nil {
		return admin.PriceOverrideImport{}, fmt.Errorf("the file is not the JSON of a list of overrides: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return admin.PriceOverrideImport{}, errors.New("the file holds more than one JSON value")
	}
	var out admin.PriceOverrideImport
	for _, e := range f.Overrides {
		out.Overrides = append(out.Overrides, e.PriceOverrideSet)
	}
	return out, nil
}

func priceImport(ctx context.Context, c *call) error {
	fs := c.flags("price import <file|-> [--json]\n\n"+
		`The file holds {"overrides": [{"model": ..., "input_micro": ..., "output_micro": ..., "max_output": ...}, ...]}, with prices in`+"\n"+
		"microdollars per million tokens, as kman price list --json prints them. It is all or nothing, and overrides it doesn't name stay.", true)
	pos, err := c.parse(fs, 1, 1)
	if err != nil {
		return err
	}
	var raw []byte
	if pos[0] == "-" {
		raw, err = io.ReadAll(c.env.Stdin)
	} else {
		raw, err = os.ReadFile(pos[0])
	}
	if err != nil {
		return err
	}
	req, err := parseImport(raw)
	if err != nil {
		return err
	}
	cl, l, err := c.connect()
	if err != nil {
		return err
	}
	got, err := cl.ImportPriceOverrides(ctx, req)
	if err != nil {
		return c.explain(err, l)
	}
	if c.json {
		return c.printJSON(got)
	}
	fmt.Fprintf(c.env.Stdout, "Imported price overrides: %d changed, %d unchanged.\n", got.Changed, got.Unchanged)
	return nil
}

// printOverrides lists the overrides after the table, where they are seen, and
// says nothing when there are none.
func printOverrides(c *call, overrides []admin.PriceOverride) {
	if len(overrides) == 0 {
		return
	}
	fmt.Fprintln(c.env.Stdout, "\nPrice overrides, which win over the table above until removed:")
	w := tabwriter.NewWriter(c.env.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SCOPE\tMODEL\tINPUT\tOUTPUT\tCACHED\tMAX OUTPUT\tNOTES")
	for _, o := range overrides {
		scope := "install"
		if o.UpstreamID != "" {
			scope = "upstream " + o.Upstream
		}
		var cached string
		if o.CachedMicro != nil {
			cached = dollars(*o.CachedMicro)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", scope, o.Model, dollars(o.InputMicro), dollars(o.OutputMicro), cached, o.MaxOutput, overrideNotes(o))
	}
	w.Flush()
}

func overrideNotes(o admin.PriceOverride) string {
	var notes []string
	if len(o.Hides) > 0 {
		notes = append(notes, "hides built-in: "+strings.Join(o.Hides, ", "))
	}
	for _, p := range []struct {
		name string
		v    *int64
	}{
		{"cache write", o.CacheWriteMicro}, {"cache write 1h", o.CacheWrite1hMicro},
		{"audio in", o.AudioInputMicro}, {"audio out", o.AudioOutputMicro}, {"audio cached", o.AudioCachedMicro},
	} {
		if p.v != nil {
			notes = append(notes, p.name+" "+dollars(*p.v))
		}
	}
	return strings.Join(notes, "; ")
}
