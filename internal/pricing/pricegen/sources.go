package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The datasets, and where the update script fetches them. Only the script
// touches the network: this program reads files (ADR 0007).
const (
	urlLiteLLM   = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	urlGenAI     = "https://raw.githubusercontent.com/pydantic/genai-prices/main/prices/new_data/v2/data.json"
	urlModelsDev = "https://models.dev/api.json"
)

// Which section of the table each dataset's provider feeds, by the preset
// names of SPEC §5. openai and anthropic are the global section: their ids are
// the models' own, and any upstream serving them falls back to these prices.
var (
	litellmProviders = map[string]string{
		"openai": "openai", "anthropic": "anthropic", "gemini": "gemini", "groq": "groq",
		"mistral": "mistral", "deepseek": "deepseek", "xai": "xai", "together_ai": "together",
		"fireworks_ai": "fireworks", "cerebras": "cerebras", "openrouter": "openrouter",
	}
	genaiProviders = map[string]string{
		"openai": "openai", "anthropic": "anthropic", "google": "gemini", "groq": "groq",
		"mistral": "mistral", "deepseek": "deepseek", "x-ai": "xai", "together": "together",
		"fireworks": "fireworks", "cerebras": "cerebras", "openrouter": "openrouter",
	}
	modelsDevProviders = map[string]string{
		"openai": "openai", "anthropic": "anthropic", "google": "gemini", "groq": "groq",
		"mistral": "mistral", "deepseek": "deepseek", "xai": "xai", "togetherai": "together",
		"fireworks-ai": "fireworks", "cerebras": "cerebras", "openrouter": "openrouter",
	}
)

// metered are the LiteLLM modes kman meters (SPEC §8). Any other mode, stated
// by LiteLLM, keeps the model out of the table whatever the other sources say.
var metered = map[string]bool{"chat": true, "responses": true, "completion": true, "embedding": true, "realtime": true}

// key names one model in one section of the table.
type key struct{ preset, model string }

// parsed is what one dataset says.
type parsed struct {
	rows     map[key]*row
	excluded map[key]bool // models it says kman doesn't meter
	aliases  [][]key      // ids it says name one model
}

func newParsed() *parsed { return &parsed{rows: map[key]*row{}, excluded: map[key]bool{}} }

// litellmPrice reads the token-price keys: a kind, then an optional
// long-context threshold, then an optional service tier.
var litellmPrice = regexp.MustCompile(`^(input_cost_per_token_cache_hit|input_cost_per_token|output_cost_per_token|cache_read_input_token_cost|cache_creation_input_token_cost_above_1hr|cache_creation_input_token_cost|input_cost_per_audio_token|output_cost_per_audio_token|cache_read_input_audio_token_cost)(?:_above_(\d+)k_tokens)?(?:_(batches|flex|priority|fast|ultrafast|balanced))?$`)

var litellmFields = map[string]field{
	"input_cost_per_token": fInput, "output_cost_per_token": fOutput,
	"cache_read_input_token_cost": fCached, "input_cost_per_token_cache_hit": fCached,
	"cache_creation_input_token_cost": fWrite, "cache_creation_input_token_cost_above_1hr": fWrite1h,
	"input_cost_per_audio_token": fAudioIn, "output_cost_per_audio_token": fAudioOut,
	"cache_read_input_audio_token_cost": fAudioCached,
}

func parseLiteLLM(raw []byte) (*parsed, error) {
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("litellm: %w", err)
	}
	out := newParsed()
	for name, fields := range doc {
		var provider, mode string
		json.Unmarshal(fields["litellm_provider"], &provider)
		json.Unmarshal(fields["mode"], &mode)
		preset, ok := litellmProviders[provider]
		if !ok {
			continue
		}
		model := strings.TrimPrefix(name, provider+"/")
		k := key{preset, model}
		if !metered[mode] {
			out.excluded[k] = true
			continue
		}
		r := &row{mode: mode}
		json.Unmarshal(fields["max_input_tokens"], &r.context)
		json.Unmarshal(fields["max_output_tokens"], &r.maxOut)
		for fname, value := range fields {
			var v float64
			if json.Unmarshal(value, &v) != nil {
				continue
			}
			switch fname {
			case "input_cost_per_second":
				if mode == "realtime" {
					r.perMinute = micro(v, 60*1e6)
				}
				continue
			case "input_cost_per_text_message":
				r.perText = micro(v, 1e6)
				continue
			case "file_search_cost_per_1k_calls":
				r.addTool("file_search", micro(v, 1e3))
				continue
			case "code_interpreter_cost_per_session":
				r.addTool("code_interpreter", micro(v, 1e6))
				continue
			case "google_maps_grounding_cost_per_query":
				r.addTool("maps_grounding", micro(v, 1e6))
				continue
			}
			m := litellmPrice.FindStringSubmatch(fname)
			if m == nil {
				continue
			}
			s := &r.base
			switch m[3] {
			case "":
			case "batches":
				s = r.tier("batch")
			default:
				s = r.tier(m[3])
			}
			c := &s[litellmFields[m[1]]]
			price := micro(v, perToken)
			if m[2] == "" {
				c.base = higher(c.base, &price)
				continue
			}
			n, err := strconv.ParseInt(m[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("litellm: %s: %s: %w", name, fname, err)
			}
			if c.steps == nil {
				c.steps = map[int64]int64{}
			}
			c.steps[n*1000] = price
		}
		var search map[string]float64
		if json.Unmarshal(fields["search_context_cost_per_query"], &search) == nil {
			for _, v := range search {
				r.addTool("web_search", micro(v, 1e6))
			}
		}
		// Bands by range replace a flat price: dashscope and a few others.
		var tiered []struct {
			Input  *float64  `json:"input_cost_per_token"`
			Output *float64  `json:"output_cost_per_token"`
			Cached *float64  `json:"cache_read_input_token_cost"`
			Range  []float64 `json:"range"`
		}
		if json.Unmarshal(fields["tiered_pricing"], &tiered) == nil {
			for _, t := range tiered {
				if len(t.Range) == 0 {
					continue
				}
				r.base[fInput].setBand(int64(t.Range[0]), t.Input, perToken)
				r.base[fOutput].setBand(int64(t.Range[0]), t.Output, perToken)
				r.base[fCached].setBand(int64(t.Range[0]), t.Cached, perToken)
			}
		}
		r.inherit()
		mergeInto(out.rows, k, r)
	}
	return out, nil
}

func (c *curve) setBand(from int64, dollars *float64, scale float64) {
	if dollars == nil {
		return
	}
	price := micro(*dollars, scale)
	if from <= 0 {
		c.base = &price
		return
	}
	if c.steps == nil {
		c.steps = map[int64]int64{}
	}
	c.steps[from] = price
}

func (r *row) addTool(name string, fee int64) {
	if r.tools == nil {
		r.tools = map[string]int64{}
	}
	r.tools[name] = max(r.tools[name], fee)
}

// genaiPrices is one price object in genai-prices' v2 feed.
type genaiPrices map[string]json.RawMessage

var genaiFields = map[string]field{
	"input_mtok": fInput, "output_mtok": fOutput, "cache_read_mtok": fCached, "cache_write_mtok": fWrite,
	"cache_write_1h_mtok": fWrite1h, "input_audio_mtok": fAudioIn, "output_audio_mtok": fAudioOut, "cache_audio_read_mtok": fAudioCached,
}

// apply adds these prices to s, keeping the higher where it already has one:
// prices that apply together, such as time windows, are all possible.
func (p genaiPrices) apply(s *schedule, r *row) error {
	for name, value := range p {
		f, ok := genaiFields[name]
		if !ok {
			var v float64
			if json.Unmarshal(value, &v) != nil || r == nil {
				continue
			}
			switch name {
			case "audio_hours":
				r.perMinute = max(r.perMinute, micro(v/60, 1e6))
			case "input_text_messages_kcount":
				r.perText = max(r.perText, micro(v/1000, 1e6))
			case "web_searches_kcount":
				r.addTool("web_search", micro(v/1000, 1e6))
			case "storage_searches_kcount":
				r.addTool("file_search", micro(v/1000, 1e6))
			}
			continue
		}
		var c curve
		var flat float64
		if json.Unmarshal(value, &flat) == nil {
			price := micro(flat, perMtok)
			c.base = &price
		} else {
			var tiered struct {
				Base  float64 `json:"base"`
				Tiers []struct {
					Start int64   `json:"start"`
					Price float64 `json:"price"`
				} `json:"tiers"`
			}
			if err := json.Unmarshal(value, &tiered); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			price := micro(tiered.Base, perMtok)
			c.base = &price
			for _, t := range tiered.Tiers {
				if c.steps == nil {
					c.steps = map[int64]int64{}
				}
				c.steps[t.Start] = micro(t.Price, perMtok)
			}
		}
		s[f] = mergeCurves(s[f], c)
	}
	return nil
}

type genaiConstraint struct {
	StartDate string `json:"start_date"`
	StartTime string `json:"start_time"`
}

type genaiConstrained struct {
	Constraint *genaiConstraint `json:"constraint"`
	Prices     genaiPrices      `json:"prices"`
}

func (c genaiConstrained) start() string {
	if c.Constraint == nil {
		return ""
	}
	return c.Constraint.StartDate
}

func (c genaiConstrained) windowed() bool { return c.Constraint != nil && c.Constraint.StartTime != "" }

// inForce picks the prices that apply today from a model's price history: the
// newest ones already started, and, when those are only time windows, the
// all-day price they sit on. Time windows all count, so the peak one sets the
// price.
func inForce(history []genaiConstrained, today string) []genaiConstrained {
	var latest string
	for _, h := range history {
		if s := h.start(); s <= today && s > latest {
			latest = s
		}
	}
	var out []genaiConstrained
	allDay := false
	for _, h := range history {
		if h.start() == latest {
			out = append(out, h)
			allDay = allDay || !h.windowed()
		}
	}
	if allDay {
		return out
	}
	var base *genaiConstrained
	for _, h := range history {
		if !h.windowed() && h.start() <= today && (base == nil || h.start() >= base.start()) {
			base = &h
		}
	}
	if base != nil {
		out = append(out, *base)
	}
	return out
}

// genaiIDs lists the exact ids a match clause names. A prefix or substring
// match names no id, and kman matches exactly.
func genaiIDs(m map[string]json.RawMessage, into map[string]bool) {
	var s string
	if json.Unmarshal(m["equals"], &s) == nil && s != "" {
		into[s] = true
	}
	var or []map[string]json.RawMessage
	if json.Unmarshal(m["or"], &or) == nil {
		for _, c := range or {
			genaiIDs(c, into)
		}
	}
}

func parseGenAI(raw []byte, now time.Time) (*parsed, error) {
	var doc []struct {
		ID     string `json:"id"`
		Models []struct {
			ID            string                     `json:"id"`
			Match         map[string]json.RawMessage `json:"match"`
			ContextWindow int64                      `json:"context_window"`
			Prices        json.RawMessage            `json:"prices"`
			Variants      []struct {
				When struct {
					ServiceTier string `json:"service_tier"`
					Processing  string `json:"processing"`
				} `json:"when"`
				Constraint *genaiConstraint `json:"constraint"`
				Prices     genaiPrices      `json:"prices"`
			} `json:"price_variants"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("genai-prices: %w", err)
	}
	today := now.UTC().Format(time.DateOnly)
	out := newParsed()
	for _, provider := range doc {
		preset, ok := genaiProviders[provider.ID]
		if !ok {
			continue
		}
		for _, m := range provider.Models {
			var history []genaiConstrained
			var single genaiPrices
			if json.Unmarshal(m.Prices, &single) == nil {
				history = []genaiConstrained{{Prices: single}}
			} else if err := json.Unmarshal(m.Prices, &history); err != nil {
				return nil, fmt.Errorf("genai-prices: %s/%s: %w", provider.ID, m.ID, err)
			}
			r := &row{context: m.ContextWindow}
			for _, h := range inForce(history, today) {
				if err := h.Prices.apply(&r.base, r); err != nil {
					return nil, fmt.Errorf("genai-prices: %s/%s: %w", provider.ID, m.ID, err)
				}
			}
			tierStart := map[string]string{}
			for _, v := range m.Variants {
				name := v.When.ServiceTier
				switch {
				case v.When.Processing == "batch" && name == "":
					name = "batch"
				case v.When.Processing != "":
					continue
				}
				start := ""
				if v.Constraint != nil {
					start = v.Constraint.StartDate
				}
				if name == "" || name == "default" || len(v.Prices) == 0 || start > today || start < tierStart[name] {
					continue
				}
				tierStart[name] = start
				t := schedule{}
				if err := v.Prices.apply(&t, nil); err != nil {
					return nil, fmt.Errorf("genai-prices: %s/%s: %w", provider.ID, m.ID, err)
				}
				*r.tier(name) = t
			}
			r.inherit()
			ids := map[string]bool{m.ID: true}
			genaiIDs(m.Match, ids)
			var group []key
			for id := range ids {
				mergeInto(out.rows, key{preset, id}, r)
				group = append(group, key{preset, id})
			}
			out.aliases = append(out.aliases, group)
		}
	}
	return out, nil
}

func parseModelsDev(raw []byte) (*parsed, error) {
	type costs struct {
		Input      *float64 `json:"input"`
		Output     *float64 `json:"output"`
		CacheRead  *float64 `json:"cache_read"`
		CacheWrite *float64 `json:"cache_write"`
		AudioIn    *float64 `json:"input_audio"`
		AudioOut   *float64 `json:"output_audio"`
	}
	var doc map[string]struct {
		Models map[string]struct {
			Limit struct {
				Context int64 `json:"context"`
				Output  int64 `json:"output"`
			} `json:"limit"`
			Cost *struct {
				costs
				ContextOver200k *costs `json:"context_over_200k"`
				Tiers           []struct {
					costs
					Tier struct {
						Type string `json:"type"`
						Size int64  `json:"size"`
					} `json:"tier"`
				} `json:"tiers"`
			} `json:"cost"`
			Experimental struct {
				Modes map[string]struct {
					Cost *costs `json:"cost"`
				} `json:"modes"`
			} `json:"experimental"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("models.dev: %w", err)
	}
	set := func(s *schedule, from int64, c *costs) {
		s[fInput].setBand(from, c.Input, perMtok)
		s[fOutput].setBand(from, c.Output, perMtok)
		s[fCached].setBand(from, c.CacheRead, perMtok)
		s[fWrite].setBand(from, c.CacheWrite, perMtok)
		s[fAudioIn].setBand(from, c.AudioIn, perMtok)
		s[fAudioOut].setBand(from, c.AudioOut, perMtok)
	}
	out := newParsed()
	for provider, body := range doc {
		preset, ok := modelsDevProviders[provider]
		if !ok {
			continue
		}
		for id, m := range body.Models {
			r := &row{context: m.Limit.Context, maxOut: m.Limit.Output}
			// models.dev holds DeepSeek's off-peak price, half the peak one,
			// so its DeepSeek prices are never used; its limits are.
			if m.Cost != nil && preset != "deepseek" {
				set(&r.base, 0, &m.Cost.costs)
				// context_over_200k is an older spelling of a tier. Where the
				// real tier is listed, its size is the threshold (gpt-5.4's is
				// 272K), and the name says nothing.
				if c := m.Cost.ContextOver200k; c != nil && len(m.Cost.Tiers) == 0 {
					set(&r.base, 200000, c)
				}
				for _, t := range m.Cost.Tiers {
					if t.Tier.Type == "context" {
						set(&r.base, t.Tier.Size, &t.costs)
					}
				}
				for name, mode := range m.Experimental.Modes {
					if mode.Cost != nil {
						set(r.tier(name), 0, mode.Cost)
					}
				}
			}
			r.inherit()
			mergeInto(out.rows, key{preset, id}, r)
		}
	}
	return out, nil
}

// mergeInto adds r under k, keeping the conservative value where the same
// dataset lists a model twice, as LiteLLM's unprefixed and prefixed rows do.
func mergeInto(rows map[key]*row, k key, r *row) {
	if have := rows[k]; have != nil {
		rows[k] = mergeRows(have, r)
		return
	}
	rows[k] = r
}

// parseAll reads the three datasets.
func parseAll(in inputs, now time.Time) (map[string]*parsed, error) {
	lite, err := parseLiteLLM(in.litellm)
	if err != nil {
		return nil, err
	}
	gen, err := parseGenAI(in.genai, now)
	if err != nil {
		return nil, err
	}
	dev, err := parseModelsDev(in.modelsdev)
	if err != nil {
		return nil, err
	}
	return map[string]*parsed{"litellm": lite, "genai-prices": gen, "models.dev": dev}, nil
}

// checkSources refuses a dataset with no rows for kman's providers: its format
// has changed, or the download was cut short.
func checkSources(in inputs, now time.Time) error {
	all, err := parseAll(in, now)
	if err != nil {
		return err
	}
	var problems []error
	for _, name := range []string{"litellm", "genai-prices", "models.dev"} {
		if len(all[name].rows) == 0 {
			problems = append(problems, fmt.Errorf("%s has no rows for any preset", name))
		}
	}
	return errors.Join(problems...)
}
