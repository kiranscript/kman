# Research: WebSocket proxying for LLM realtime APIs (kirank55/kman#5)

Date: 2026-11-02. All facts below come from first-party docs or source fetched on this date. Where a source did not settle a point, the text says so.

**Question.** How do LLM realtime / WebSocket APIs authenticate, report usage, and behave? What would a virtual-key proxy have to do to forward them safely?

Background: keman's spec lists "WebSockets, including the OpenAI Realtime API" and "Auth in the query string" as non-goals (`/tmp/keman-ref/keman/beta.md`, lines 46-48). Its spend model reserves cost before forwarding and reconciles when the response ends (`beta.md`, lines 112-117). kman v1 includes WebSockets, so it has to carry that model over to long-lived sessions.

---

## 1. Provider survey (keman's 21 presets)

| Provider | WebSocket / realtime surface | Where the key goes | Usage reporting | Limits |
|---|---|---|---|---|
| **OpenAI Realtime** | `wss://api.openai.com/v1/realtime?model=…`, plus `?intent=transcription`, `/v1/realtime/translations`, and the WebRTC `POST /v1/realtime/calls` | Server: `Authorization: Bearer`. Browser: subprotocols `["realtime", "openai-insecure-api-key.<ek_…>"]`. WebRTC: Bearer `ek_…` or a standard key on the SDP POST | `response.done.response.usage` per Response (tokens by text/audio/image, cached). Transcription usage in `conversation.item.input_audio_transcription.completed`. Also emits `rate_limits.updated` | Session max 60 min. `ek_` TTL 10–7200 s, default 600 s |
| **OpenAI GPT-Live** (new voice product) | `wss://api.openai.com/v1/live/sessions`, WebRTC via `POST /v1/live/sessions`, sideband `wss://…/v1/live/sessions/{id}/attach` | `Authorization: Bearer` (project key, server-side only; the docs read describe no client secret) | `session.usage.updated` gives **cumulative** `usage.seconds` (don't sum them). Final count in `session.closed`. Backend tokens arrive in nested `response.event` → `response.completed.usage` | Billed per second ($0.05/min for `gpt-live-1`). WebRTC init bills 15 s up front, credited later. `session.closed.reason` can be `expired`, but the docs read give no duration value. SIP calls max 2 h |
| **OpenAI Responses WebSocket mode** | WS to `/v1/responses` (`response.create` events, `stream_id` multiplexing) | `Authorization: Bearer` | Normal Responses `usage` on `response.completed` | 60-min connection (`websocket_connection_limit_reached`). 16 in-flight responses and 32 named streams per connection |
| **Gemini Live** | `wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent` (ephemeral: `…BidiGenerateContentConstrained`) | API key in **query string** `?key=` (raw-WS docs) or `x-goog-api-key` header (what the python-genai SDK sends). Ephemeral: `?access_token=` or `Authorization: Token <auth_tokens/…>` | `usageMetadata` attached to server messages: prompt/response/cached/tool/thoughts token counts, broken down by modality. The reference doesn't say whether it is cumulative or per-turn, so test it | Audio-only session 15 min, audio+video 2 min, unless context compression is on. Connection about 10 min, with a `goAway{timeLeft}` warning first. Resumption handles valid 2 h |
| **xAI** | Voice Agent `wss://api.x.ai/v1/realtime?model=grok-voice-latest` (OpenAI-Realtime-shaped events), plus `wss://api.x.ai/v1/stt`, `wss://api.x.ai/v1/tts`, `wss://api.x.ai/v1/responses` | Server: `Authorization: Bearer`. Browser: subprotocol `xai-client-secret.<token>` | Voice is billed **per minute** ($0.08/min) plus $0.004 per text input. The pages read document no usage event, so the proxy would have to time the connection itself | Responses WS max 59 min. Voice history dropped after 30 min idle. Ephemeral `POST /v1/realtime/client_secrets {expires_after:{seconds}}`, which takes no session constraints |
| **Together AI** | `wss://api.together.ai/v1/realtime?model=…&input_audio_format=pcm_s16le_16000` (streaming transcription) | `Authorization: Bearer` plus `OpenAI-Beta: realtime=v1`. The docs' own example also uses query `authorization=Bearer …` and subprotocol `openai-insecure-api-key.<key>` | Not documented on the page | — |
| **Mistral** | `wss://api.mistral.ai/v1/audio/transcriptions/realtime?model=voxtral-mini-transcribe-realtime-…` (Voxtral realtime STT, recommended through the SDK) | SDK API key; the page doesn't show the handshake header | Not documented on the page | — |
| **Fireworks** | Streaming ASR used to live on separate hosts (`audio-streaming*.fireworks.ai`). The current docs index no longer lists it and the old API page returns 404 | — | — | Off the preset host either way |
| **Cloudflare** | Realtime WS lives on AI Gateway (`wss://gateway.ai.cloudflare.com/v1/<acct>/<gw>/…`, `cf-aig-authorization` header), not on the `api.cloudflare.com/client/v4` preset | — | — | — |
| **Slack** | Socket Mode: `apps.connections.open` (Bearer `xapp-` app token) returns a one-off `wss://wss.slack.com/link/?ticket=…` URL. The WS host differs from the API host and the URL refreshes | Ticket in the URL | n/a (request-counted profile) | Connection refreshes regularly |
| Anthropic, OpenRouter, Groq, DeepSeek, Cerebras | No WebSocket API in their docs indexes (no `websocket`/`realtime` hits in llms.txt / docs home) | — | — | — |
| Stripe, GitHub, Twilio, SendGrid, Resend, Postmark, Linear, Notion | No outbound WS API relevant to a credential proxy. Twilio Media Streams connect *into* the customer's server, so the direction is reversed | — | — | — |

### Sources
- OpenAI WebSockets guide (Realtime + GPT-Live): https://developers.openai.com/api/docs/guides/voice-websockets.md. Shows the `Authorization: Bearer` header, the browser subprotocol `"openai-insecure-api-key." + EPHEMERAL_KEY`, `wss://api.openai.com/v1/live/sessions`, and "Voice-duration updates are cumulative snapshots; do not add them together".
- OpenAI WebRTC guide: https://developers.openai.com/api/docs/guides/voice-webrtc.md. Covers the unified interface (server POSTs SDP to `/v1/realtime/calls` with a standard key) and ephemeral `POST /v1/realtime/client_secrets`.
- Client secret reference: https://developers.openai.com/api/reference/resources/realtime/subresources/client_secrets/methods/create.md. Says `expires_after.seconds` is "between `10` and `7200`… default 600", that session config attached to the secret "can also be overridden by the client connection", and gives the value format `ek_…`.
- Server-side controls / sideband: https://developers.openai.com/api/docs/guides/voice-server-controls.md. The `Location: /v1/realtime/calls/rtc_…` header leads to the sideband at `wss://api.openai.com/v1/realtime?call_id=…`. GPT-Live's equivalent is `wss://api.openai.com/v1/live/sessions/{session_id}/attach`.
- Realtime conversations ("maximum duration of a Realtime session is **60 minutes**"): https://developers.openai.com/api/docs/guides/realtime-conversations.md
- Cost guide (per-Response `response.done` usage, audio 1 token/100 ms in and 1/50 ms out, transcription usage, GPT-Live `session.usage.updated`, the 15 s WebRTC init charge): https://developers.openai.com/api/docs/guides/voice-latency-cost.md
- GPT-Live sessions (close reasons, graceful close, usage unconfirmed if the socket drops before `session.closed`): https://developers.openai.com/api/docs/guides/live-conversations.md. Model page: https://developers.openai.com/api/docs/models/gpt-live-1.md
- Telephony (hangup endpoints `POST /v1/live/sessions/{id}/hangup` and `/v1/realtime/calls/{id}/hangup`, SIP 2 h limit): https://developers.openai.com/api/docs/guides/voice-sip.md
- VAD (`turn_detection.create_response: true` means the server creates Responses on its own): https://developers.openai.com/api/docs/guides/realtime-vad.md
- Transcription / translation endpoints: https://developers.openai.com/api/docs/guides/realtime-transcription.md and https://developers.openai.com/api/docs/guides/realtime-translation.md
- Responses WebSocket mode: https://developers.openai.com/api/docs/guides/websocket-mode.md
- Gemini Live raw WebSocket guide (`?key=`, `…Constrained?access_token=`): https://ai.google.dev/gemini-api/docs/live-api/get-started-websocket
- Gemini Live API reference (`usageMetadata`, `goAway.timeLeft`, `AuthToken.expireTime` default 30 min and under 20 h, `newSessionExpireTime` default 60 s, `uses` default 1, `Authorization: Token`): https://ai.google.dev/api/live
- Gemini session management (15 min / 2 min / about 10 min connection, 2 h resumption): https://ai.google.dev/gemini-api/docs/live-api/session-management
- Gemini ephemeral tokens (`POST /v1beta/auth_tokens`, `liveConnectConstraints` locking): https://ai.google.dev/gemini-api/docs/live-api/ephemeral-tokens
- python-genai sends `x-goog-api-key` as a header (`_api_client.py` line ~1061) and switches to `Authorization: Token …` plus `BidiGenerateContentConstrained` for `auth_tokens/` keys (`live.py` lines ~1048-1058): https://github.com/googleapis/python-genai/blob/main/google/genai/live.py
- xAI speech-to-speech: https://docs.x.ai/developers/model-capabilities/audio/speech-to-speech.md. Ephemeral tokens: https://docs.x.ai/developers/model-capabilities/audio/ephemeral-tokens.md. WebSocket mode (59 min): https://docs.x.ai/developers/advanced-api-usage/websocket-mode.md. Pricing: https://docs.x.ai/developers/models.md
- Together streaming transcription: https://docs.together.ai/docs/inference/transcription/streaming.md
- Mistral realtime transcription: https://docs.mistral.ai/studio/audio/speech_to_text/realtime_transcription.md
- Fireworks docs index (no streaming-ASR entry): https://docs.fireworks.ai/llms.txt
- Cloudflare AI Gateway realtime WebSockets: https://developers.cloudflare.com/ai-gateway/usage/websockets-api/realtime-api/
- Slack Socket Mode: https://docs.slack.dev/apis/events-api/using-socket-mode.md
- Indexes checked with no WebSocket API found: https://console.groq.com/llms.txt, https://docs.claude.com/llms.txt, https://openrouter.ai/docs/llms.txt, https://inference-docs.cerebras.ai/llms.txt, https://api-docs.deepseek.com/

---

## 2. Go forwarding mechanics

### `httputil.ReverseProxy` (Go 1.27 stdlib source, `net/http/httputil/reverseproxy.go`)
- It keeps `Connection: Upgrade` / `Upgrade` on the outbound request. On a `101` response it calls `ModifyResponse`, checks that the upgrade types match, **hijacks** the client connection, writes the 101 response, and then runs two `io.Copy` goroutines (`switchProtocolCopier`). The relay is an opaque byte pipe.
- So it can upgrade and forward out of the box, but it **cannot see WebSocket messages**. The only hook is `ModifyResponse`: at 101 time `res.Body` is the backend `io.ReadWriteCloser`, and a wrapper there could tee the bytes. That means writing your own RFC 6455 frame parser: masking on client frames, fragmentation, control frames, and inflating `permessage-deflate` with context takeover. On a cap breach the proxy can't inject a clean close frame; it can only drop the TCP connection.
- The `Director`/`Rewrite` step can strip `Sec-WebSocket-Protocol` or `Sec-WebSocket-Extensions` and add the real `Authorization` header. That covers handshake-only credential swapping.
- HTTP/2: Go's bundled http2 disables extended CONNECT (RFC 8441) by default ("Disable extended CONNECT by default for now. Issue #71128", `net/http/internal/http2/http2.go`). Browsers therefore fall back to HTTP/1.1 Upgrade, which ReverseProxy and hijack support.

### Terminating with a WS library (recommended)
- `github.com/coder/websocket`: `Accept(w, r, &AcceptOptions{Subprotocols, OriginPatterns, CompressionMode})` on the client side, and `Dial(ctx, url, &DialOptions{HTTPHeader, Subprotocols, CompressionMode})` upstream. It reads whole messages and closes with a status code. Its default read limit is **32768 bytes per message**, which is too small for base64 audio and `session.update` payloads; raise it with `SetReadLimit`. Source: https://github.com/coder/websocket (`accept.go`, `dial.go`, `read.go`). `gorilla/websocket` is an equivalent alternative.
- The flow: authenticate the virtual key from the handshake → apply policy → `Dial` upstream with the real credential → run two message pumps. The upstream→client pump `json.Unmarshal`s only the `type` field and the usage-bearing events (`response.done`, `session.usage.updated`, `session.closed`, `conversation.item.input_audio_transcription.completed`, Gemini `usageMetadata`, `goAway`). Everything else passes through as bytes.
- Turn compression off on both legs (or let the library handle it) so parsing stays cheap. Both legs then speak uncompressed frames, and compression stays negotiated per leg.
- Close codes on enforcement: `1008` (policy violation), or an app-defined `4xxx` code with a reason, sent after an `error` event shaped like the provider's so SDKs surface it.

### Handshake rules a proxy must honour
1. **Key in the subprotocol.** Browsers can't set headers, so OpenAI clients send `Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.<key>` and xAI clients send `xai-client-secret.<key>`. Together's docs use the same OpenAI form. kman must parse `kmn_…` out of those tokens, **never echo the key-bearing token back** as the selected subprotocol (echo `realtime`), drop it from the upstream handshake, and send the real key as a header. Treat `Sec-WebSocket-Protocol` as an auth header for log redaction.
2. **Key in the query string.** Gemini Live documents `?key=`, and Gemini ephemeral tokens use `?access_token=`. keman ruled out query-string auth and forwards query strings as-is (`beta.md`, line 157). kman would need a per-preset exception: accept `kmn_` in `key`/`access_token`, remove it, and either add `x-goog-api-key` upstream (the SDK already sends that header) or rewrite the query. Logs must never contain the query string.
3. **Upstream URL and host are fixed by the credential**, as today. Gemini Live needs a new path allowlist entry on the `gemini` preset (`/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent*`), because the current preset covers only `/v1beta/openai`.
4. **Origin.** WebSocket has no CORS. If browsers are expected to hold `kmn_` keys, restrict `Origin` per key (an `OriginPatterns`-style allowlist) or require short-lived kman-minted keys (see §4).

---

## 3. Accounting and mid-session cap enforcement

keman reserves, forwards, then reconciles once per HTTP request. In a socket session the billable unit is smaller than the connection:

- **OpenAI Realtime (token-billed).** The unit is the Response. Reserve when the client sends `response.create`, or when a `response.created` event appears for a server-VAD response. Size the reservation as current conversation input (the last `usage.input_tokens` plus new items) × input price, plus `max_output_tokens` × output price. Reconcile on `response.done`. Server VAD (`create_response: true`) starts Responses without a client event, so a proxy that sees a cap breach coming can't veto the next Response. It has three options: (a) close the socket once committed spend plus the next worst case would exceed the cap, which bounds overshoot to one Response; (b) rewrite the client's `session.update` to clamp `max_output_tokens` (a session-level and response-level field); or (c) force `create_response:false` so every Response goes through a client `response.create` the proxy can gate. Option (c) changes app behaviour. Add input-transcription cost from the `…transcription.completed` usage.
- **GPT-Live / xAI voice (duration-billed).** Treat it as a meter. GPT-Live's `session.usage.updated.usage.seconds` is cumulative, so store the latest value and don't add values together. xAI documents no usage event, so meter wall-clock time from the 101 to the close. Check the cap on every update or tick and close at the boundary. Charge the 15 s GPT-Live WebRTC init. Delegated backend tokens arrive in nested `response.event` → `response.completed`; count each response ID once.
- **Gemini Live.** Price each `usageMetadata` by modality (`promptTokensDetails` / `responseTokensDetails`). Whether the counts are cumulative or per-turn has to be confirmed against a live session before kman relies on either reading. Resumed sessions arrive on new connections, so tie them together by resumption handle.
- **Abnormal close.** If the socket drops before `response.done` or `session.closed`, charge the open reservation and mark the event estimated. This matches keman's existing rule (ASSUMPTIONS.md #11) and OpenAI's own guidance to "mark final usage as unconfirmed".
- **Hold a concurrency slot per open session**, since providers rate-limit by concurrent sessions (e.g. `gpt-live-1` tiers 50/300/500). Flush spend counters during the session too, not only at the end, because sessions last up to 60 min.
- **Duration ceilings to plan for:** Realtime 60 min; Responses WS 60 min (xAI 59); Gemini connection about 10 min with `goAway` and session resumption; Gemini audio sessions 15 min without compression. kman's own server timeouts (`ReadTimeout`/`WriteTimeout`/idle) must not kill hijacked or accepted sockets earlier than these.

---

## 4. Ephemeral tokens: can kman mint them for a virtual key?

- **Mechanically, yes.** The mint endpoints are plain authenticated HTTP POSTs: OpenAI `POST /v1/realtime/client_secrets` (and `/v1/realtime/translations/client_secrets`), Gemini `POST /v1beta/auth_tokens` (the SDK uses v1alpha), and xAI `POST /v1/realtime/client_secrets`. kman can forward them with the real key like any other path.
- **The catch.** The `ek_…`, `auth_tokens/…` or xAI secret that comes back is a **real provider credential**. The browser then connects straight to the provider, so kman loses usage visibility, caps and revocation. The provider-side limits are weak: OpenAI TTL 10 s–2 h, and the session config "can also be overridden by the client connection". Gemini is the strongest: `uses` default 1, `newSessionExpireTime` 60 s, `liveConnectConstraints` and field locks. xAI offers TTL only.
- **Recommended design: kman-native ephemeral keys.** kman mints a short-lived, scoped child `kmn_` key (TTL, single use, model, parent cap) that the browser presents to **kman's** WS endpoint, via the subprotocol or `?key=`. kman holds the real key upstream. Enforcement, accounting and revocation are unchanged.
- **WebRTC (OpenAI Realtime and GPT-Live).** Media can't pass through an HTTP proxy, but kman can broker the session using the unified interface:
  1. The browser POSTs its SDP to kman with a child key.
  2. kman forwards it to `/v1/realtime/calls` (or `/v1/live/sessions`) with the real key and reads the `call_id` / `session.id` from the `Location` header or JSON body.
  3. kman opens a **sideband** WS (`wss://api.openai.com/v1/realtime?call_id=…` or `/v1/live/sessions/{id}/attach`) with the real key to read `response.done` / `session.usage.updated`.
  4. On a cap breach kman calls the **hangup** endpoint (`POST /v1/realtime/calls/{id}/hangup`, `POST /v1/live/sessions/{id}/hangup`).

  Audio then flows browser↔OpenAI directly. The real key never reaches the client, and kman still meters and enforces. If kman instead hands out an `ek_`, the browser's own SDP POST goes directly to OpenAI and kman never learns the `call_id`.

---

## 5. Recommendations for kman v1 scope
1. Terminate WebSockets with a library (coder/websocket) rather than byte-piping through `ReverseProxy`, because accounting and clean policy closes need message boundaries.
2. Use per-preset "realtime adapters" that declare the WS paths, how to extract the key (header / subprotocol / query), how to inject the upstream key, and the usage-event parsers. Priority: OpenAI Realtime (+ transcription/translation), OpenAI Responses WS, GPT-Live, Gemini Live, xAI voice. Together and Mistral STT could later reuse the OpenAI-shaped and duration-meter adapters.
3. Use per-Response reservation for token-billed sessions and a duration meter for time-billed ones. Close with 1008/4xxx at the cap, and count the documented one-Response overshoot as known overshoot.
4. Don't proxy provider-ephemeral tokens as the default browser story. Offer kman child keys, plus brokered WebRTC with a sideband and hangup.
5. Out of scope or not applicable: Slack Socket Mode (ticket URL on another host), Cloudflare AI Gateway (other host), Fireworks (no current docs), and the providers with no WS API.

## Open points (not settled by sources)
- Whether Gemini `usageMetadata` is cumulative or per-turn.
- GPT-Live's maximum session duration (the docs name the `expired` reason without giving a number).
- Usage events on xAI voice; the pages read show none.
- The Mistral realtime handshake auth header (the docs only show SDK usage).
- Whether GPT-Live offers a browser client-secret flow (the pages read show only server-brokered SDP).
