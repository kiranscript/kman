# Child keys, not provider ephemeral tokens

Browsers open realtime sessions with **child keys**: short-lived `ek_kmn_` keys (ADR 0001) minted from a parent virtual key. kman never hands out a provider ephemeral token such as OpenAI's `ek_…` or xAI's client secret. When an app calls a provider's client-secret endpoint through kman (`POST /v1/realtime/client_secrets` and its xAI counterpart), kman intercepts the call. It answers in the provider's response shape, with a child key in place of the real token. The official SDK browser flow then works without changes and connects to kman: the OpenAI browser SDKs accept the child key because it starts with `ek_`.

A provider ephemeral token is a real credential. A browser holding one talks straight to the provider, and kman loses usage visibility, spend caps, and revocation for that session. Provider-side limits are weak: OpenAI lets the client override the session config, and xAI offers only a TTL.

## Considered options

- **Forward the mint endpoints as ordinary requests.** Rejected. The cheapest option, but every browser session would bypass the product.
- **Refuse the mint endpoints and offer only a kman-native mint route.** Rejected as the only path. It would push every app to rewrite its client-secret call. kman offers the native route as well.

## Consequences

- A child key defaults to 60 seconds to connect and to a single session. It inherits its parent's policy and can only narrow it (model, a lower cap, an Origin allowlist). Its spend counts against the parent.
- The official OpenAI browser SDKs do check the prefix: `openai-node`'s realtime WebSocket and `@openai/agents-realtime` (WebSocket and WebRTC) refuse any credential not starting with `ek_` unless the app sets `dangerouslyAllowBrowser` or `useInsecureApiKey`. Child keys carry `ek_` for that reason, so apps never need those flags. The pinned-SDK e2e suite runs both browser flows with no opt-out flag to keep it true.
- xAI has no official browser SDK and no prefix check. Its browser flow passes the child key in the `xai-client-secret.` subprotocol.
- For WebRTC, kman brokers the SDP exchange itself, using the real key, so the browser never needs a provider token there either.
