package gateway

import (
	"errors"
	"regexp"
	"strings"

	"github.com/kirank55/kman/internal/meter"
	"github.com/kirank55/kman/internal/profiles"
)

// reserve takes a reservation against the key's spend before anything is
// forwarded, or refuses the request: over a cap, unmetered where the key may send
// only metered requests (SPEC §8), or with no way to be sure of what the key has
// spent. The upstream is priced by its ID, Preset and PriceMultiplierPPM, never
// its Name, which the operator chooses.
func (g *Gateway) reserve(x *exchange) *refusal {
	key, up := x.route.Key, x.route.Upstream
	if err := key.Compiled.Err(); err != nil {
		// A policy that can't be read is not an empty one: that would free a capped key.
		// (The policy stage refuses such a key first; this keeps reserve safe on its own.)
		g.opts.Log.Error("a key's policy can't be read", "key", key.ID, "err", err)
		return &refusal{code: codeStoreUnavailable, message: "kman cannot read this virtual key's policy right now."}
	}
	// A request must be metered when its key's policy says so: it has a cap or a
	// model allowlist.
	strict := key.Compiled.RequiresMetering()
	dailyCap, monthlyCap := key.Compiled.Caps()
	if g.opts.Meter == nil {
		if strict {
			return &refusal{code: codeStoreUnavailable}
		}
		return nil
	}
	profile := profiles.For(up.Profile)
	asked := profile.Ask(x.body)
	res, err := g.opts.Meter.Reserve(g.opts.Now(), meter.Ask{
		KeyID: key.ID, UpstreamID: up.ID, Preset: up.Preset, MultiplierPPM: up.PriceMultiplierPPM,
		Meters: profile.Meters(), Metered: asked.Metered, Model: asked.Model, MaxTokens: asked.MaxTokens,
		ServiceTier: asked.ServiceTier, RequestBytes: int64(len(x.body)),
		DailyCapMicro: dailyCap, MonthlyCapMicro: monthlyCap, Strict: strict,
	})
	switch {
	case err == nil:
		x.res = res
		return nil
	case errors.Is(err, meter.ErrCapReached):
		return &refusal{code: codeSpendCapReached}
	case errors.Is(err, meter.ErrUnmetered):
		return &refusal{code: codeUnmeteredRequest}
	case errors.Is(err, meter.ErrStoreUnavailable):
		return &refusal{code: codeStoreUnavailable}
	}
	if unknown, ok := errors.AsType[*meter.UnknownPriceError](err); ok {
		return &refusal{code: codeUnknownModelPrice, message: unknownPriceMessage(unknown.Model, up.Name)}
	}
	g.opts.Log.Error("the meter refused a request for a reason it does not know", "key", key.ID, "err", err)
	return &refusal{code: codeStoreUnavailable}
}

// settle charges the request what it cost, or gives its reservation back, and
// hands the usage read from the response on. It runs for every request, refused
// or not, and once: a response with a length settles before its last byte goes.
func (g *Gateway) settle(x *exchange) {
	if x.settled {
		return
	}
	x.settled = true
	var usage profiles.Usage
	if x.usage != nil {
		usage = x.usage.Usage()
		if g.opts.OnUsage != nil {
			g.opts.OnUsage(x.route, usage)
		}
	}
	if x.res == nil {
		return
	}
	g.opts.Meter.Settle(g.opts.Now(), x.res, meter.Outcome{
		Status: x.status, Usage: usage, Broken: x.abort,
		Free: profiles.For(x.route.Upstream.Profile).Free(x.r.URL.Path),
	})
}

// safeModel is a model id that is fine to repeat in a message and to type in a
// shell: what the providers use, without a quote or a space.
var safeModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)

// unknownPriceMessage says which model has no price, and the command that gives
// it one. A model name that isn't a plain id is the app's to choose, and isn't
// repeated.
func unknownPriceMessage(model, upstream string) string {
	const set = "--input <dollars per million tokens> --output <dollars per million tokens> --max-output <tokens>"
	if !safeModel.MatchString(model) {
		return "kman has no price for this model on upstream " + upstream + ". Ask the operator to set one with kman price set."
	}
	return "kman has no price for model " + model + " on upstream " + upstream + ". Set one with: " +
		strings.Join([]string{"kman price set", model, "--upstream", upstream}, " ") + " " + set
}
