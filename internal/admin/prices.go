package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PriceTable is the built-in price table as admin serves it.
type PriceTable struct {
	GeneratedAt time.Time
	StaleAfter  time.Duration // past this age, ask for an upgrade
	Models      []PriceModel
}

// PriceModel is one model in the price table. Prices are per million tokens
// unless a field says otherwise, and money is in microdollars.
type PriceModel struct {
	Preset            string         `json:"preset,omitempty" doc:"The preset this entry belongs to. Absent for a global entry, which every upstream falls back to."`
	Model             string         `json:"model" doc:"The model id, matched exactly."`
	Mode              string         `json:"mode" doc:"chat, responses, completion, embedding or realtime."`
	InputMicro        int64          `json:"input_micro"`
	OutputMicro       int64          `json:"output_micro"`
	CachedMicro       *int64         `json:"cached_micro,omitempty" doc:"Cached input. Absent: billed at the input price."`
	CacheWriteMicro   *int64         `json:"cache_write_micro,omitempty" doc:"Cache writes. Absent: billed at the input price."`
	AudioInputMicro   *int64         `json:"audio_input_micro,omitempty" doc:"Audio input. Absent: billed at the input price."`
	AudioOutputMicro  *int64         `json:"audio_output_micro,omitempty" doc:"Audio output. Absent: billed at the output price."`
	MaxOutput         int64          `json:"max_output" doc:"The most tokens one response can output, which sizes reservations."`
	Reviewed          bool           `json:"reviewed" doc:"Whether a person checked max_output against the provider's docs."`
	ContextWindow     int64          `json:"context_window,omitempty"`
	LongContext       []PriceBand    `json:"long_context,omitempty" doc:"Prices for a whole request whose input is over a size."`
	ServiceTiers      []string       `json:"service_tiers,omitempty" doc:"The service tiers with their own prices."`
	PerMinuteMicro    int64          `json:"per_minute_micro,omitempty" doc:"Voice: per minute, billed per second."`
	PerTextInputMicro int64          `json:"per_text_input_micro,omitempty" doc:"Voice: per text input."`
	ToolFees          []PriceToolFee `json:"tool_fees,omitempty" doc:"Server-tool fees, per call."`
}

// PriceBand is a long-context tier.
type PriceBand struct {
	AboveTokens int64 `json:"above_tokens"`
	InputMicro  int64 `json:"input_micro"`
	OutputMicro int64 `json:"output_micro"`
}

// PriceToolFee is the fee for one call of a server tool.
type PriceToolFee struct {
	Tool  string `json:"tool"`
	Micro int64  `json:"micro"`
}

// PriceQuery narrows the price table.
type PriceQuery struct {
	Preset string `json:"-" query:"preset" doc:"Only this preset's entries, or global for the global ones."`
	Model  string `json:"-" query:"model" doc:"Only models whose id contains this, in any case."`
}

// PriceDataStatus says how old the price table is. Age never changes what kman
// enforces.
type PriceDataStatus struct {
	GeneratedAt time.Time `json:"generated_at"`
	AgeSeconds  int64     `json:"age_seconds"`
	AgeDays     int64     `json:"age_days"`
	Stale       bool      `json:"stale" doc:"Whether the table is past 60 days old."`
	Notice      string    `json:"notice,omitempty" doc:"What to tell the operator when it is stale."`
}

// PriceList is the price table and how old it is.
type PriceList struct {
	Status PriceDataStatus `json:"status"`
	Models []PriceModel    `json:"models"`
}

// Status says how old the table is at now.
func (t PriceTable) Status(now time.Time) PriceDataStatus {
	age := max(now.Sub(t.GeneratedAt), 0)
	st := PriceDataStatus{
		GeneratedAt: t.GeneratedAt,
		AgeSeconds:  int64(age / time.Second),
		AgeDays:     int64(age / (24 * time.Hour)),
		Stale:       age > t.StaleAfter,
	}
	if st.Stale {
		st.Notice = fmt.Sprintf("price data is %d days old, upgrade kman", st.AgeDays)
	}
	return st
}

// ListPrices returns the price table, narrowed by q, and how old it is.
func (s *Service) ListPrices(ctx context.Context, q PriceQuery) (PriceList, error) {
	t, err := s.priceTable(ctx)
	if err != nil {
		return PriceList{}, err
	}
	out := PriceList{Status: t.Status(s.now()), Models: []PriceModel{}}
	want := strings.ToLower(q.Model)
	for _, m := range t.Models {
		section := m.Preset
		if section == "" {
			section = "global"
		}
		if (q.Preset == "" || q.Preset == section) && strings.Contains(strings.ToLower(m.Model), want) {
			out.Models = append(out.Models, m)
		}
	}
	return out, nil
}

// PriceDataStatus says how old the price table is.
func (s *Service) PriceDataStatus(ctx context.Context) (PriceDataStatus, error) {
	t, err := s.priceTable(ctx)
	if err != nil {
		return PriceDataStatus{}, err
	}
	return t.Status(s.now()), nil
}

func (s *Service) priceTable(ctx context.Context) (PriceTable, error) {
	if _, err := caller(ctx); err != nil {
		return PriceTable{}, err
	}
	if s.prices == nil {
		return PriceTable{}, errors.New("admin: this service has no price table")
	}
	return s.prices(), nil
}
