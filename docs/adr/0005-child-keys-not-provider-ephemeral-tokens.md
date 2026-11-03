# Child keys, not provider ephemeral tokens

Browsers open realtime sessions with **child keys**: short-lived `kmn_` keys minted from a parent virtual key. kman never hands out a provider ephemeral token such as OpenAI's `ek_…` or xAI's client secret. When an app calls a provider's client-secret endpoint through kman (`POST /v1/realtime/client_secrets` and its xAI counterpart), kman intercepts the call. It answers in the provider's response shape, with a child key in place of the real token, so the official SDK browser flow works without changes and connects to kman.

A provider ephemeral token is a real credential. A browser holding one talks straight to the provider, and kman loses usage visibility, spend caps, and revocation for that session. Provider-side limits are weak: OpenAI lets the client override the session config, and xAI offers only a TTL.

## Considered options

- **Forward the mint endpoints as ordinary requests.** Rejected. The cheapest option, but every browser session would bypass the product.
- **Refuse the mint endpoints and offer only a kman-native mint route.** Rejected as the only path. It would push every app to rewrite its client-secret call. kman offers the native route as well.

## Consequences

- A child key defaults to 60 seconds to connect and to a single session. It inherits its parent's policy and can only narrow it (model, a lower cap, an Origin allowlist). Its spend counts against the parent.
- Responses from the intercepted endpoints carry a `kmn_` value where SDKs expect `ek_…`. Apps that parse the token's prefix would break. Whether any official SDK checks the prefix is not yet verified. The realtime verification task checks it.
- For WebRTC, kman brokers the SDP exchange itself, using the real key, so the browser never needs a provider token there either.
