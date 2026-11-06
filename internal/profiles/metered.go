package profiles

import "net/http"

// openAI is the OpenAI profile, and that of every service speaking its protocol.
type openAI struct{ generic }

func (openAI) UsageReader(h http.Header) *UsageReader { return newUsageReader(h, (*reported).openAI) }

// anthropic is the Anthropic profile.
type anthropic struct{ generic }

func (anthropic) UsageReader(h http.Header) *UsageReader {
	return newUsageReader(h, (*reported).anthropic)
}
