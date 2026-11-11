package profiles

import (
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
)

// Usage is what a response reports about itself. Token counts are disjoint: an
// input token that was cached or audio is counted there and not in InputTokens,
// whichever way the provider counts it, so that nothing is priced twice.
type Usage struct {
	// Seen says the response reported usage. Model and ServiceTier can be set
	// without it.
	Seen        bool
	Model       string // the model the response names
	ServiceTier string // the tier it reports having run at

	InputTokens, OutputTokens            int64
	CachedTokens                         int64
	CacheWriteTokens, CacheWrite1hTokens int64
	AudioInputTokens, AudioOutputTokens  int64
	AudioCachedTokens                    int64

	Tools []ToolCalls // the server-tool calls in the response

	// ReportedCost says the provider reported this response's cost, in
	// ReportedMicro microdollars, rounded up. Zero can be a reported cost.
	ReportedCost  bool
	ReportedMicro int64
}

// ToolCalls counts the calls of one server tool, named as the price table names
// its fee: web_search, file_search, code_interpreter.
type ToolCalls struct {
	Name  string
	Calls int64
}

// UsageReader reads usage out of a response as it passes through. It holds no
// more of the response than the few small fields it wants, however long the
// response is and however it is split into chunks. It is for one response and
// one goroutine.
type UsageReader struct {
	normalize func(*reported) Usage
	json      fieldScanner
	isStream  bool
	lines     sseLines // for a stream
	got       reported
}

// newUsageReader returns a reader for a response kman can read, or nil. Under a
// content encoding it can't: kman asks for none, so a response that comes
// encoded anyway goes through unread.
func newUsageReader(h http.Header, normalize func(*reported) Usage) *UsageReader {
	if enc := h.Get("Content-Encoding"); enc != "" && !strings.EqualFold(enc, "identity") {
		return nil
	}
	media, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	media = strings.ToLower(strings.TrimSpace(media))
	r := &UsageReader{normalize: normalize}
	switch {
	case media == "text/event-stream":
		r.isStream = true
	case media == "application/json" || strings.HasSuffix(media, "+json"):
	default:
		return nil
	}
	return r
}

// Feed reads the next piece of the response.
func (r *UsageReader) Feed(p []byte) {
	if r.isStream {
		r.lines.feed(p, &r.json, &r.got)
		return
	}
	r.json.feed(p, &r.got)
}

// Usage is what the response has reported so far.
func (r *UsageReader) Usage() Usage { return r.normalize(&r.got) }

// reported is what a response has said, as it said it. A later event's fields
// replace an earlier one's, and a field it leaves out stays: an Anthropic stream
// gives its input counts at the start, and its output counts at the end.
type reported struct {
	model, tier wireString
	usage       wireUsage
	tools       [len(toolNames)]int64
}

var toolNames = [...]string{"web_search", "file_search", "code_interpreter"}

// wireString is a string field. A chunk of a stream usually repeats the last
// value, which then costs nothing to read.
type wireString struct {
	raw, value string
}

func (s *wireString) set(raw []byte) {
	if string(raw) == s.raw {
		return
	}
	s.raw = string(raw)
	if v, ok := decodeString(raw); ok && v != "" {
		s.value = v
	}
}

func decodeString(raw []byte) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var v string
	err := json.Unmarshal(raw, &v)
	return v, err == nil
}

// wireUsage is the usage objects of every provider on these profiles.
type wireUsage struct {
	PromptTokens     count `json:"prompt_tokens"`
	CompletionTokens count `json:"completion_tokens"`
	InputTokens      count `json:"input_tokens"`
	OutputTokens     count `json:"output_tokens"`

	PromptDetails     inputDetails  `json:"prompt_tokens_details"`
	InputDetails      inputDetails  `json:"input_tokens_details"`
	CompletionDetails outputDetails `json:"completion_tokens_details"`
	OutputDetails     outputDetails `json:"output_tokens_details"`
	CacheHit          count         `json:"prompt_cache_hit_tokens"` // DeepSeek

	// Anthropic
	CacheRead     count `json:"cache_read_input_tokens"`
	CacheCreation count `json:"cache_creation_input_tokens"`
	CacheByTTL    struct {
		FiveMinutes count `json:"ephemeral_5m_input_tokens"`
		OneHour     count `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerTools struct {
		WebSearch count `json:"web_search_requests"`
	} `json:"server_tool_use"`
	ServiceTier lenientString `json:"service_tier"`
	Speed       lenientString `json:"speed"`

	Cost  cost  `json:"cost"`              // OpenRouter: dollars
	Ticks count `json:"cost_in_usd_ticks"` // xAI: 10^10 to the dollar
}

type inputDetails struct {
	Cached       count `json:"cached_tokens"`
	Audio        count `json:"audio_tokens"`
	CachedDetail struct {
		Audio count `json:"audio_tokens"`
	} `json:"cached_tokens_details"`
}

type outputDetails struct {
	Audio count `json:"audio_tokens"`
}

// A count is a token count. One that isn't a usable number counts as absent, so
// that a provider's odd field costs that field and not the whole usage.
type count struct {
	n  int64
	ok bool
}

// maxCount keeps a nonsense count from overflowing anything downstream.
const maxCount = 1 << 53

func (c *count) UnmarshalJSON(b []byte) error {
	f, err := strconv.ParseFloat(string(b), 64) // a string in quotes doesn't parse
	if err != nil || f < 0 || f > maxCount {
		return nil
	}
	c.n, c.ok = int64(math.Ceil(f)), true
	return nil
}

// or is this count, or the other when this one is absent.
func (c count) or(other count) count {
	if c.ok {
		return c
	}
	return other
}

// lenientString is a string field, ignored when it is anything else.
type lenientString string

func (s *lenientString) UnmarshalJSON(b []byte) error {
	if v, ok := decodeString(b); ok {
		*s = lenientString(v)
	}
	return nil
}

// cost is a cost in dollars, as a provider reports it.
type cost struct {
	micro int64
	ok    bool
}

const (
	maxCostDollars = 1e9
	maxCostLen     = 64 // with a number in range, this bounds the exponent too
)

// UnmarshalJSON reads the number exactly, because the float nearest to $0.0038
// is a hair over it, and rounding up would charge another microdollar.
func (c *cost) UnmarshalJSON(b []byte) error {
	if len(b) > maxCostLen {
		return nil
	}
	f, err := strconv.ParseFloat(string(b), 64)
	if err != nil || f < 0 || f > maxCostDollars {
		return nil
	}
	if f == 0 {
		c.micro, c.ok = 0, true // also spares SetString an exponent like 1e-999999
		return nil
	}
	exact, ok := new(big.Rat).SetString(string(b))
	if !ok {
		return nil
	}
	exact.Mul(exact, big.NewRat(1_000_000, 1))
	micro := new(big.Int).Div(exact.Num(), exact.Denom()) // Denom is positive
	if !exact.IsInt() {
		micro.Add(micro, big.NewInt(1))
	}
	c.micro, c.ok = micro.Int64(), true
	return nil
}

// ticksPerMicrodollar: xAI reports 10^10 ticks to the dollar.
const ticksPerMicrodollar = 10_000

// common is what both profiles read alike.
func (g *reported) common() Usage {
	u := Usage{Model: g.model.value, ServiceTier: g.tier.value, Seen: g.usage != (wireUsage{})}
	if u.ServiceTier == "" {
		u.ServiceTier = string(g.usage.ServiceTier)
	}
	switch {
	case g.usage.Ticks.ok:
		u.ReportedCost, u.ReportedMicro = true, (g.usage.Ticks.n+ticksPerMicrodollar-1)/ticksPerMicrodollar
	case g.usage.Cost.ok:
		u.ReportedCost, u.ReportedMicro = true, g.usage.Cost.micro
	}
	for i, calls := range g.tools {
		if calls > 0 {
			u.Tools = append(u.Tools, ToolCalls{toolNames[i], calls})
		}
	}
	return u
}

// openAI turns what a response reported into disjoint counts. prompt_tokens (or
// the Responses API's input_tokens) includes cached and audio tokens, and
// completion_tokens includes audio.
func (g *reported) openAI() Usage {
	u, w := g.common(), &g.usage
	in := w.PromptDetails
	if !in.Cached.ok && !in.Audio.ok {
		in = w.InputDetails
	}
	cached := in.Cached.or(w.CacheHit).n
	u.AudioCachedTokens = in.CachedDetail.Audio.n
	u.CachedTokens = max(cached-u.AudioCachedTokens, 0)
	u.AudioInputTokens = max(in.Audio.n-u.AudioCachedTokens, 0)
	u.InputTokens = max(w.PromptTokens.or(w.InputTokens).n-u.CachedTokens-u.AudioCachedTokens-u.AudioInputTokens, 0)

	out := w.CompletionDetails
	if !out.Audio.ok {
		out = w.OutputDetails
	}
	u.AudioOutputTokens = out.Audio.n
	u.OutputTokens = max(w.CompletionTokens.or(w.OutputTokens).n-u.AudioOutputTokens, 0)
	return u
}

// anthropic turns what a response reported into Usage. Its counts are disjoint
// already, and cache_creation_input_tokens is the two TTLs together.
func (g *reported) anthropic() Usage {
	u, w := g.common(), &g.usage
	u.InputTokens, u.OutputTokens, u.CachedTokens = w.InputTokens.n, w.OutputTokens.n, w.CacheRead.n
	u.CacheWrite1hTokens = w.CacheByTTL.OneHour.n
	u.CacheWriteTokens = max(w.CacheByTTL.FiveMinutes.n, w.CacheCreation.n-u.CacheWrite1hTokens)
	if w.ServerTools.WebSearch.n > 0 {
		u.Tools = append(u.Tools, ToolCalls{"web_search", w.ServerTools.WebSearch.n})
	}
	// Fast mode is a price of its own, reported apart from the service tier.
	if w.Speed == "fast" {
		u.ServiceTier = "fast"
	}
	return u
}

// setUsage merges a usage object into what's been reported.
func (g *reported) setUsage(raw []byte) {
	if len(raw) == 0 || raw[0] != '{' {
		return
	}
	// A value of the wrong type sets an error and leaves the rest decoded, and
	// count and its like never make one. Bad syntax decodes nothing.
	_ = json.Unmarshal(raw, &g.usage)
}

// setTool counts an output item of a Responses API response by its type.
func (g *reported) setTool(raw []byte) {
	switch string(raw) {
	case `"web_search_call"`:
		g.tools[0]++
	case `"file_search_call"`:
		g.tools[1]++
	case `"code_interpreter_call"`:
		g.tools[2]++
	}
}
