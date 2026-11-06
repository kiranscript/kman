package profiles

import (
	"slices"

	"github.com/kirank55/kman/internal/registry"
)

const bearer = "Authorization: Bearer {key}"

// header is a Spec's auth in the header style.
func header(template string) registry.Auth {
	return registry.Auth{Style: registry.AuthHeader, Header: template}
}

// presets is keman's 21 presets (SPEC §5), the LLM platforms and then other
// APIs, as data. Changing one means raising its Version, or no upstream ever
// sees the change; a test holds this to testdata/presets.json.
//
// The LLM allowlists leave out batch, files, fine-tune, and multipart audio
// and image routes (SPEC §7), which keman allowed. An LLM preset's Name is also
// its section in the price table, so renaming one loses its prices. Every preset
// is on 443 over TLS, which the Presets function fills in.
var presets = []registry.Preset{
	{Name: "openai", Title: "OpenAI", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.openai.com", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "OPENAI_API_KEY", SDKBasePath: "/v1",
		Paths: []string{
			"POST /v1/chat/completions",
			"POST /v1/completions",
			"POST /v1/embeddings",
			"POST /v1/responses",
			"POST /v1/responses/**",
			"GET /v1/responses/**",
			"POST /v1/images/generations",
			"POST /v1/audio/speech",
			"POST /v1/moderations",
			"GET /v1/models",
			"GET /v1/models/*",
		},
	}},
	{Name: "anthropic", Title: "Anthropic", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileAnthropic, Host: "api.anthropic.com",
		Auth: header("x-api-key: {key}"), SDKEnv: "ANTHROPIC_API_KEY",
		Paths: []string{
			"POST /v1/messages",
			"POST /v1/messages/count_tokens",
			"POST /v1/complete",
			"GET /v1/models",
			"GET /v1/models/*",
		},
	}},
	{Name: "openrouter", Title: "OpenRouter", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "openrouter.ai", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "OPENROUTER_API_KEY", SDKBasePath: "/api/v1",
		Paths: []string{
			"POST /api/v1/chat/completions",
			"GET /api/v1/models",
			"GET /api/v1/models/*",
		},
	}},
	{Name: "gemini", Title: "Google Gemini (OpenAI-compatible)", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "generativelanguage.googleapis.com", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "GEMINI_API_KEY", SDKBasePath: "/v1beta/openai",
		Paths: []string{
			"POST /v1beta/openai/chat/completions",
			"POST /v1beta/openai/embeddings",
			"GET /v1beta/openai/models",
			"GET /v1beta/openai/models/**",
		},
	}},
	{Name: "groq", Title: "Groq", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.groq.com", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "GROQ_API_KEY", SDKBasePath: "/openai/v1",
		Paths: []string{
			"POST /openai/v1/chat/completions",
			"POST /openai/v1/responses",
			"GET /openai/v1/models",
			"GET /openai/v1/models/**",
		},
	}},
	{Name: "mistral", Title: "Mistral", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.mistral.ai",
		Auth: header(bearer), SDKEnv: "MISTRAL_API_KEY", SDKBasePath: "/v1",
		Paths: []string{
			"POST /v1/chat/completions",
			"POST /v1/fim/completions",
			"POST /v1/embeddings",
			"GET /v1/models",
			"GET /v1/models/*",
		},
	}},
	{Name: "deepseek", Title: "DeepSeek", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.deepseek.com", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "DEEPSEEK_API_KEY",
		Paths: []string{
			"POST /chat/completions",
			"POST /v1/chat/completions",
			"GET /models",
			"GET /v1/models",
		},
	}},
	{Name: "xai", Title: "xAI (Grok)", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.x.ai", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "XAI_API_KEY", SDKBasePath: "/v1",
		Paths: []string{
			"POST /v1/chat/completions",
			"POST /v1/responses",
			"GET /v1/responses/*",
			"GET /v1/models",
			"GET /v1/models/*",
		},
	}},
	{Name: "together", Title: "Together AI", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.together.xyz", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "TOGETHER_API_KEY", SDKBasePath: "/v1",
		Paths: []string{
			"POST /v1/chat/completions",
			"POST /v1/completions",
			"POST /v1/embeddings",
			"GET /v1/models",
		},
	}},
	{Name: "fireworks", Title: "Fireworks AI", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.fireworks.ai", InjectUsage: true,
		Auth: header(bearer), SDKEnv: "FIREWORKS_API_KEY", SDKBasePath: "/inference/v1",
		Paths: []string{
			"POST /inference/v1/chat/completions",
			"POST /inference/v1/completions",
			"POST /inference/v1/embeddings",
		},
	}},
	{Name: "cerebras", Title: "Cerebras", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileOpenAI, Host: "api.cerebras.ai",
		Auth: header(bearer), SDKEnv: "CEREBRAS_API_KEY", SDKBasePath: "/v1",
		Paths: []string{
			"POST /v1/chat/completions",
			"POST /v1/completions",
			"GET /v1/models",
			"GET /v1/models/*",
		},
	}},

	{Name: "stripe", Title: "Stripe", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.stripe.com",
		Auth: header(bearer), SDKEnv: "STRIPE_SECRET_KEY",
		Paths: []string{
			"POST /v1/payment_intents",
			"GET /v1/payment_intents/*",
			"POST /v1/payment_intents/*",
			"POST /v1/payment_intents/*/confirm",
			"POST /v1/payment_intents/*/cancel",
			"POST /v1/checkout/sessions",
			"GET /v1/checkout/sessions/*",
			"POST /v1/customers",
			"GET /v1/customers",
			"GET /v1/customers/*",
			"POST /v1/customers/*",
			"GET /v1/charges",
			"GET /v1/charges/*",
			"GET /v1/prices",
			"GET /v1/products",
			"GET /v1/subscriptions",
			"GET /v1/subscriptions/*",
			"GET /v1/invoices",
			"GET /v1/invoices/*",
		},
	}},
	{Name: "github", Title: "GitHub", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.github.com",
		Auth: header(bearer), SDKEnv: "GITHUB_TOKEN",
		Paths: []string{
			"GET /user",
			"GET /user/repos",
			"GET /repos/**",
			"GET /orgs/*/repos",
			"GET /search/**",
			"POST /repos/*/*/issues",
			"PATCH /repos/*/*/issues/*",
			"POST /repos/*/*/issues/*/comments",
			"POST /repos/*/*/pulls",
			"POST /repos/*/*/pulls/*/reviews",
		},
	}},
	{Name: "slack", Title: "Slack", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "slack.com",
		Auth: header(bearer), SDKEnv: "SLACK_BOT_TOKEN", SDKBasePath: "/api",
		Paths: []string{
			"POST /api/auth.test",
			"POST /api/chat.postMessage",
			"POST /api/chat.postEphemeral",
			"POST /api/chat.update",
			"POST /api/reactions.add",
			"* /api/conversations.list",
			"* /api/conversations.history",
			"* /api/conversations.replies",
			"* /api/users.info",
			"POST /api/files.getUploadURLExternal",
			"POST /api/files.completeUploadExternal",
		},
	}},
	{Name: "twilio", Title: "Twilio", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.twilio.com",
		Auth: registry.Auth{Style: registry.AuthBasic}, SDKEnv: "TWILIO_AUTH_TOKEN",
		Paths: []string{
			"POST /2010-04-01/Accounts/*/Messages.json",
			"GET /2010-04-01/Accounts/*/Messages.json",
			"GET /2010-04-01/Accounts/*/Messages/*",
			"POST /2010-04-01/Accounts/*/Calls.json",
			"GET /2010-04-01/Accounts/*/Calls/*",
		},
	}},
	{Name: "sendgrid", Title: "SendGrid", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.sendgrid.com",
		Auth: header(bearer), SDKEnv: "SENDGRID_API_KEY",
		Paths: []string{
			"POST /v3/mail/send",
			"GET /v3/templates",
			"GET /v3/templates/*",
		},
	}},
	{Name: "resend", Title: "Resend", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.resend.com",
		Auth: header(bearer), SDKEnv: "RESEND_API_KEY",
		Paths: []string{
			"POST /emails",
			"POST /emails/batch",
			"GET /emails/*",
		},
	}},
	{Name: "postmark", Title: "Postmark", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.postmarkapp.com",
		Auth: header("X-Postmark-Server-Token: {key}"), SDKEnv: "POSTMARK_SERVER_TOKEN",
		Paths: []string{
			"POST /email",
			"POST /email/batch",
			"POST /email/withTemplate",
			"POST /email/batchWithTemplates",
			"GET /messages/outbound",
			"GET /messages/outbound/*/details",
		},
	}},
	{Name: "cloudflare", Title: "Cloudflare", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.cloudflare.com",
		Auth: header(bearer), SDKEnv: "CLOUDFLARE_API_TOKEN", SDKBasePath: "/client/v4",
		Paths: []string{
			"GET /client/v4/user/tokens/verify",
			"GET /client/v4/zones",
			"GET /client/v4/zones/*",
			"GET /client/v4/zones/*/dns_records",
			"GET /client/v4/zones/*/dns_records/*",
			"POST /client/v4/zones/*/dns_records",
			"PATCH /client/v4/zones/*/dns_records/*",
			"DELETE /client/v4/zones/*/dns_records/*",
			"POST /client/v4/zones/*/purge_cache",
		},
	}},
	{Name: "linear", Title: "Linear", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.linear.app",
		Auth: header("Authorization: {key}"), SDKEnv: "LINEAR_API_KEY",
		Paths: []string{
			"POST /graphql",
		},
	}},
	{Name: "notion", Title: "Notion", Version: 1, Spec: registry.Spec{
		Profile: registry.ProfileGeneric, Host: "api.notion.com",
		Auth: header(bearer), SDKEnv: "NOTION_TOKEN",
		Paths: []string{
			"GET /v1/users/me",
			"POST /v1/search",
			"POST /v1/pages",
			"GET /v1/pages/*",
			"PATCH /v1/pages/*",
			"GET /v1/databases/*",
			"POST /v1/databases/*/query",
			"GET /v1/data_sources/*",
			"POST /v1/data_sources/*/query",
			"GET /v1/blocks/*",
			"GET /v1/blocks/*/children",
			"PATCH /v1/blocks/*/children",
		},
	}},
}

// Presets returns a copy of every preset in display order. admin may not
// import this package (ADR 0006), so the composition root passes this to it.
func Presets() []registry.Preset {
	out := slices.Clone(presets)
	for i := range out {
		out[i].Spec.Port, out[i].Spec.TLS = 443, true
		out[i].Spec.Paths = slices.Clone(out[i].Spec.Paths)
	}
	return out
}
