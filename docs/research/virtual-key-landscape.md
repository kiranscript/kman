# Landscape of virtual-key and leak-containment tools

Ticket: kirank55/kman#3. Researched 2026-11-02. Every URL below was accessed on 2026-11-02.

**Question.** What do comparable tools do for virtual keys, leak containment and decoy credentials that keman doesn't? Which of those ideas belong in kman's solo tier (self-hosted, one operator)?

**Baseline.** keman's features come from `/tmp/keman-ref/keman/README.md` and `beta.md`:

- Virtual keys in the form `kmn_<id>_<secret>`, stored hashed. New keys expire after 90 days by default.
- Per-key CIDR and country allowlists, path and model allowlists, an RPM limit, and daily and monthly caps in USD.
- Canary keys that return a fake 401 and raise a critical alert.
- Baselined detections:
  - rate spike and spend spike
  - new country and new ASN
  - repeated allowlist violations
  - a burst of upstream auth errors
  - use of a disabled or revoked key
- Opt-in auto-disable, with a dry-run mode.
- Alerts to Slack and to HMAC-signed webhooks, with the CLI commands to respond.
- `key rotate --grace`, `exposure`, offline `scan --history`, and `canary create`, which prints lines ready to plant in a `.env` file.

**Method.** Sources are official docs, the vendors' own repos, and API references. A few pages were read through a summarising fetch tool, and some through raw downloads (GitHub raw files and `gh api`). Where a claim rests on a search-result summary of a primary page rather than a direct read, it is marked *(via search summary)*.

---

## 1. LLM gateways

### LiteLLM proxy (open source, with Enterprise add-ons)

**Key model**
- `/key/generate` creates a virtual key. It needs Postgres (`DATABASE_URL`), and the master key must start with `sk-`.
- Keys carry `models`, `metadata`, `aliases`, `user_id`, `team_id` and `duration`.
- Ownership can be a user or a team.
- `litellm_key_header_name` lets the proxy read the key from a custom header.
- Source: https://docs.litellm.ai/docs/proxy/virtual_keys

**Limits and policy**
- Budgets:
  - `max_budget` and `budget_duration`
  - `budget_limits`, a list of windows that apply at the same time (for example daily and monthly)
  - Budgets per key, user, team, team member and end customer
  - Per-model `model_max_budget` (Enterprise)
- Rate limits: `tpm_limit`, `rpm_limit`, `max_parallel_requests`, and per-model TPM and RPM.
- Spend is reserved before the provider call. `default_estimated_output_tokens` covers requests that omit `max_tokens`.
- `allowed_routes` restricts which route groups a key can reach.
- `upperbound_key_generate_params` rejects keys that exceed caps, such as a maximum duration.
- `temp_budget_increase` with `temp_budget_expiry` gives a temporary budget raise.
- Budgets are not enforced without a database.
- Sources: https://docs.litellm.ai/docs/proxy/users, https://docs.litellm.ai/docs/proxy/virtual_keys

**IP restriction**
- `general_settings.allowed_ips` is a proxy-wide list, not a per-key one, and it is **Enterprise-only**. A blocked request gets a 403.
- Source: https://docs.litellm.ai/docs/proxy/ip_address

**Detection and alerting**
- Channels: Slack, Discord (through its Slack-compatible URL), MS Teams, and a beta webhook.
- Alert types: budget alerts, outages, slow and hanging requests, and spend reports.
- Optional management alerts: key created, updated or deleted.
- Webhook events:
  - `budget_crossed`
  - `soft_budget_crossed` (a per-key `soft_budget`)
  - `threshold_crossed`, at 85% and 95%
  - `projected_limit_exceeded`
  - `key_created` and `key_rotated`
- **There is no anomaly or leak detection.** Thresholds are fixed.
- Source: https://docs.litellm.ai/docs/proxy/alerting
- The `hide_secrets` guardrail redacts secrets in prompts using detect-secrets. It is Enterprise-only and checks requests only. *(via search summary)* Source: https://docs.litellm.ai/docs/proxy/guardrails/secret_detection

**Leak response UX**
- `/key/block` and `/key/unblock` turn a key off and on.
- `/key/{key}/regenerate` takes a `grace_period` such as `"48h"`, or revokes immediately when it is left out.
- `auto_rotate` with `rotation_interval` rotates on a schedule.
- **Rotation, including scheduled rotation, is Enterprise-only.**
- Source: https://docs.litellm.ai/docs/proxy/virtual_keys

### Portkey (SaaS; virtual keys now folded into Model Catalog)

**Key model**
- "Virtual Keys" stored the provider credentials. They have been upgraded to Model Catalog and Integrations, which use the model format `@provider-slug/model`.
- Portkey API keys are what apps hold.
- Source: https://portkey.ai/docs/product/ai-gateway/virtual-keys

**Limits and policy**
- Budgets per key, in USD (minimum $1) or in tokens. They reset weekly, monthly or never.
- **Alert thresholds** send email before the hard cap is reached.
- Rate limits by requests or tokens, per minute, hour or day.
- Budgets are for the Enterprise plan and some Pro customers.
- Source: https://portkey.ai/docs/product/administration/enforce-budget-and-rate-limit
- No IP restrictions are documented.

**Expiry and rotation** *(via search summary of the API reference)*
- `expires_at` must stay within the maximum TTL set for the org or workspace.
- `POST …/api-keys/{id}/rotate` takes `key_transition_period_ms`, with a minimum of 30 minutes. At most two secrets can be live per key.
- A rotation policy can run weekly, monthly or every N days. The key id stays the same, so usage history carries over.
- Sources: https://portkey.ai/docs/api-reference/admin-api/control-plane/api-keys/rotate-api-key, https://portkey.ai/docs/product/enterprise-offering/org-management/api-key-rotation

**Detection and alerting**
- Email notifications per key when a limit or threshold is reached, sent to org admins, the owner and the key's creator.
- No anomaly detection is documented.

### Helicone (OSS/SaaS observability gateway)

**Key model**
- Vault stores provider keys with column-level AEAD encryption.
- Apps send a one-way-hashed **proxy key**. Several proxy keys can map to one provider key.
- Source: https://www.helicone.ai/blog/vault-introduction (2023-09-13). The docs page https://docs.helicone.ai/features/advanced-usage/vault returned 404 when fetched.

**Limits and policy**
- Limits are set by the *caller* in a request header: `Helicone-RateLimit-Policy: quota;w=secs;u=request|cents;s=user|<property>`.
- An exceeded limit returns 429. Token-based limits are listed as "coming soon".
- Per-proxy-key cost and request limits were on the 2023 roadmap.
- Source: https://docs.helicone.ai/features/advanced-usage/custom-rate-limits
- This is weak containment: a caller who holds the key controls the header.

**Detection and leak response**
- None documented beyond observability dashboards.

### Cloudflare AI Gateway (SaaS)

**Key model**
- With an "Authenticated Gateway", apps send a Cloudflare API token in `cf-aig-authorization`. The token needs AI Gateway `Run` permission.
- **These tokens are account-scoped and cannot be limited to one gateway.** A token can use the BYOK keys of every gateway in the account.
- Source: https://developers.cloudflare.com/ai-gateway/configuration/authentication/
- BYOK provider keys are stored in Secrets Store under aliases, selected with `cf-aig-byok-alias`. Rotating means editing the stored key; apps need no change.
- Source: https://developers.cloudflare.com/ai-gateway/configuration/bring-your-own-keys/

**Limits and policy**
- Rate limits are per gateway, as a fixed or sliding window, and return 429.
- Source: https://developers.cloudflare.com/ai-gateway/features/rate-limiting/
- **Spend limits** cap dollars over a rolling or fixed window. They can be scoped by provider, model or custom metadata, with up to 20 rules per gateway.
  - An exceeded limit returns 429, or falls back to a cheaper model through Dynamic Routes.
  - Enforcement is approximate: spend is recorded after each request, so a burst can go over the cap.
- Source: https://developers.cloudflare.com/ai-gateway/features/spend-limits/

**Detection and leak response**
- No per-token anomaly detection or IP locks are documented.
- Leak response is revoking the Cloudflare token.

### OpenRouter (SaaS aggregator)

**Key model**
- Application keys are created through a Management API key, using `POST /api/v1/keys`.
- Management keys cannot call completions.
- Source: https://openrouter.ai/docs/features/provisioning-api-keys

**Limits and policy**
- Per-key `limit` with `limit_reset` (for example daily), and `include_byok_in_limit`.
- **Guardrails**, set per workspace, member or key:
  - `limit_usd` with `reset_interval`
  - `allowed_models`, `allowed_providers` and `allowed_data_regions`
  - When several guardrails apply, their allowlists intersect.
  - An over-budget request returns 403 with `limit_source`.
- *(Guardrails via search summary)* Source: https://openrouter.ai/docs/guides/features/guardrails
- Only management keys have a fixed expiry.

**Detection and alerting**
- "OpenRouter is a GitHub secret scanning partner, and has other methods to detect exposed keys. If we determine that your key has been compromised, you will receive an email notification."
- The docs do not say the key is disabled automatically. The user deletes it and creates a new one.
- Source: https://openrouter.ai/docs/api-reference/authentication

**Leak response UX**
- `PATCH /api/v1/keys/{hash}` with `disabled: true`, or delete the key.

---

## 2. Secrets managers and agents

### HashiCorp Vault (and Vault Agent / Vault Proxy)

**Key model**
- Tokens have the prefixes `hvs.` (service), `hvb.` (batch) and `hvr.` (recovery).
- Tokens form a parent-and-child tree. **Revoking a parent revokes every child and every lease created under it.**
- Accessors let you look up or revoke a token without being able to use it.
- Source: https://developer.hashicorp.com/vault/docs/concepts/tokens

**Limits and policy**
- `token_ttl` and `token_max_ttl`.
- `token_num_uses` limits how many times a token can be used. `secret_id_num_uses` does the same for a SecretID.
- `secret_id_ttl`.
- **CIDR binding** through `token_bound_cidrs` and `secret_id_bound_cidrs`.
- Source: https://developer.hashicorp.com/vault/api-docs/auth/approle

**Dynamic secrets**
- Each read returns a `lease_id`, with a TTL and renewal.
- `vault lease revoke -prefix aws/` revokes every secret from one backend at once. The docs pitch it for intrusions.
- Source: https://developer.hashicorp.com/vault/docs/concepts/lease

**Detection**
- **Response wrapping**: the secret is handed over in a single-use wrapping token with a short TTL.
- If the intended recipient cannot unwrap it, someone else already did, and that "is a potential security incident". The audit log confirms it.
- Source: https://developer.hashicorp.com/vault/docs/concepts/response-wrapping

**Agent and Proxy**
- Vault Proxy auto-authenticates, caches and renews tokens and leases, and writes them to sinks.
- Apps talk to a local listener, which can force the use of the auto-auth token.
- Source: https://developer.hashicorp.com/vault/docs/agent-and-proxy/proxy
- Vault manages *secrets*. It does not proxy third-party APIs, so the app still ends up holding a real (if short-lived) credential.

### Infisical

**Key model**
- Machine identities. The token-auth API takes:
  - `accessTokenTTL` and `accessTokenMaxTTL`
  - `accessTokenNumUsesLimit`
  - `accessTokenTrustedIps`, which defaults to `0.0.0.0/0`. **Trusted IPs are a paid (Pro) feature.**
- *(via search summary)* Sources: https://infisical.com/docs/documentation/platform/identities/token-auth, https://infisical.com/docs/documentation/platform/identities/universal-auth

**Dynamic secrets**
- Created when accessed, one per identity, with revocation built in.
- Advanced plan only.
- The overview page did not list providers or TTL details.
- Source: https://infisical.com/docs/documentation/platform/dynamic-secrets/overview

**Detection**
- `infisical scan` checks a directory or the git history, and can run as a pre-commit hook.
- Connected-repository scanning covers GitHub, GitLab and Bitbucket, with findings, ignore rules and custom rules.
- Source: https://infisical.com/docs/documentation/platform/secret-scanning/overview

### Doppler

**Key model**
- Service tokens (`dp.st.<env>.…`) are scoped to one config, read-only by default.
- `--max-age` makes an ephemeral token that is deleted automatically.
- Revoking is immediate and permanent. **The CLI may keep serving the last-fetched secrets from its encrypted fallback file**, so revoking does not wipe copies already on disk.
- Source: https://docs.doppler.com/docs/service-tokens

**Limits and policy**
- **Trusted IPs** (CIDR) per config, on Team and Enterprise plans.
- *(via search summary)* Source: https://docs.doppler.com/docs/trusted-ips

**Detection**
- GitHub scans for Doppler tokens, and those found in public repos "will be automatically revoked".
- *(via search summary; from a 2020 changelog, so the current behaviour is unverified)* Source: https://www.doppler.com/changes/december-2020-updates

---

## 3. Decoy and canary credentials

### Thinkst Canarytokens (free SaaS plus self-hostable OSS)

**Key model**
- About 35 token types, among them:
  - DNS and HTTP tokens
  - Office documents and PDFs
  - AWS API keys, Azure login certificates and kubeconfig files
  - MySQL dumps and WireGuard configs
  - Credit cards
  - **An MCP configuration** (`mcp.json` for `.claude/` or `.cursor/`). It alerts when a client connects, or only when a tool is called.
- Sources: docs repo https://github.com/thinkst/canarytokens-docs (`docs/guide/*.md`), https://docs.canarytokens.org/guide/getting-started.html, https://docs.canarytokens.org/guide/mcp-token.html

**AWS key tokens**
- These are real AWS keys and they work against the real AWS API, so the attacker needs no planted endpoint.
- Alerts go through AWS logging, with **a delay of 2 to 30 minutes**.
- Self-hosting needs separate AWS-side infrastructure, kept in a private repo.
- Sources: https://docs.canarytokens.org/guide/aws-keys-token.html, https://github.com/thinkst/canarytokens (README, "AWS ID infrastructure")

**Alerting**
- Email through SMTP, Mailgun, SendGrid or Mandrill.
- Webhooks formatted for Slack, Google Chat, Discord, MS Teams or generic receivers (`WebhookType` in `canarytokens/webhook_formatting.py`). Payloads carry `src_ip` and `useragent`.
- **Throttling: at most one alert per calling IP per minute.** Activity is still recorded.
- **A webhook is turned off after 5 errors in a row** (`MAX_ALERT_FAILURES`).
- The setup guide stresses **descriptive memos**: an alert has to say where the token was planted.
- Source: https://github.com/thinkst/canarytokens (README, "Alert throttling" and "Webhook failure limiting")

### GitGuardian Honeytoken (commercial; SaaS or self-hosted)

**Key model**
- Real AWS keys that grant no access.
- **They are designed to be found by the tools attackers run, such as TruffleHog and Gitleaks.** GitGuardian's own scanning does not raise incidents for them.
- Source: https://docs.gitguardian.com/honeytoken/core-concepts
- `ggshield honeytoken create-with-context` plants the token inside a realistic file in the repository's language.
- Source: https://docs.gitguardian.com/ggshield-docs/reference/honeytoken/create-with-context
- `ggshield honeytoken plant` reconciles each developer machine against the state GitGuardian wants:
  - It writes or refreshes the decoy AWS profile, and removes revoked ones.
  - It never revokes a token on its own.
  - A separate `honeytokens:check` scope stops endpoint scanning from tripping the organisation's own tokens.
- Source: https://docs.gitguardian.com/endpoint-protection/honeytoken-protection

**Events**
- Each event records the timestamp, IP address and country, user agent, and the AWS action (for example `GetCallerIdentity`).
- **IP rules** tag events (for example "office network") and can **allowlist** IP ranges. Allowlisted events are kept but do not trigger.
- The built-in tag "GitGuardian Public Monitoring IP" means the token has leaked to public GitHub, and the honeytoken is marked "Publicly exposed".
- Event intake pauses after 100 open events.
- Sources: https://docs.gitguardian.com/honeytoken/understand-events, https://docs.gitguardian.com/honeytoken/code-leakage

**Alerting**
- Email, Slack, MS Teams, ServiceNow, and custom webhooks.
- Webhook events are `honeytoken_triggered` and `new_honeytoken_event`.
- Source: https://docs.gitguardian.com/honeytoken/configure-alerts

**Response UX**
- An explicit lifecycle:
  - **Reset**, for a false alarm: the token goes back to Active and its open events are archived.
  - **Revoke**, for a real incident: the AWS key pair is deleted.
- The docs remind you to plant a replacement.
- Source: https://docs.gitguardian.com/honeytoken/respond

### Tracebit (commercial, with a free Community Edition)

**Key model**
- Enterprise canaries:
  - AWS session tokens, Azure service principals and GCP service-account keys
  - SSH keys, API keys, session cookies and passwords
  - Artifacts: `.env` files, HAR files, Terraform state and config files
  - "Perimeter sensors": decoy login portals, package registries and **MCP servers** on your own domain
- Source: https://tracebit.com/platform/credentials-and-artifacts
- The Community CLI (free, MIT licence) deploys five kinds to a workstation:
  - AWS credentials in `~/.aws/credentials`
  - SSH keys in `~/.ssh`
  - Browser cookies
  - Password-manager entries
  - Emails
- **The installer sets up background refresh, "so they stay convincing".**
- It alerts on *use*, not on reads.
- Source: https://github.com/tracebit-com/tracebit-community-cli (README)

**Positioning**
- The README pushes canaries to **catch AI coding agents that go out of scope**. One of its examples is Claude Code finding and trying a canary AWS key on a laptop.
- A free GitHub Action injects short-lived canary credentials into CI jobs.
- Source: same README; https://github.com/tracebit-com/tracebit-community-action

### Adjacent: GitHub secret scanning partner program

- A provider registers its token regexes and a verify endpoint. GitHub POSTs matches signed with ECDSA P-256.
- The endpoint must answer 2xx within 15 seconds.
- **The recommended format is a fixed prefix plus a 32-bit checksum.**
- *(via search summary)* Sources: https://docs.github.com/en/code-security/tutorials/secret-scanning-partner-program, https://docs.github.com/en/site-policy/github-terms/github-secret-scanning-partner-program-agreement

---

## 4. Comparison at a glance

| | Per-key IP/CIDR lock | Country lock | Expiry | Rotate with grace | Spend cap | Soft/threshold alert | Behavioural detection | Auto-disable | Decoys | Offline scan |
|---|---|---|---|---|---|---|---|---|---|---|
| **keman** | yes, free | yes | 90-day default | yes, free | daily/monthly | no (spike rule only) | yes (baselined) | yes, with dry-run | canary virtual keys | yes, with history |
| LiteLLM | global only, Enterprise | no | `duration` | Enterprise | multi-window | yes (soft, 85/95%, projected) | no | no (block is manual) | no | no |
| Portkey | not documented | no | `expires_at` | yes (≥30 min) | Enterprise/Pro | yes (email) | no | no | no | no |
| Helicone | no | no | no | no | caller-set header | no | no | no | no | no |
| Cloudflare AIG | no | no | no | n/a | per gateway (approximate) | no | no | no | no | no |
| OpenRouter | no | no | management keys only | no | per key, plus guardrails | no | no (GitHub-partner leak email) | no | no | no |
| Vault | yes (`token_bound_cidrs`) | no | TTL/max TTL | n/a (leases) | n/a | n/a | wrap-interception signal | n/a | no | no |
| Infisical | paid | no | TTL | n/a | n/a | n/a | no | no | no | yes |
| Doppler | per config, Team plan | no | `--max-age` | n/a | n/a | n/a | GitHub auto-revoke | n/a | no | no |
| Canarytokens | — | — | — | — | — | — | — | — | ~35 types | — |
| GitGuardian HT | — | — | — | — | — | — | — | — | AWS only, with context | ggshield |
| Tracebit | — | — | — | — | — | — | — | — | many, auto-refreshed | — |

---

## 5. A structural gap in keman's canaries

Canarytokens, GitGuardian and Tracebit all mint credentials that **work against the real provider endpoint** (AWS) and observe their use on the provider side. A thief needs nothing else.

keman's canary is a `kmn_…` key. It fires **only if the thief also uses the planted base URL**. The README says so: "The base URL must reach your Keman proxy, or the canary cannot fire."

A thief or scanner who pairs `OPENAI_API_KEY=kmn_…` with `api.openai.com` gets OpenAI's own 401, and keman never sees it. Worse, a `kmn_` prefix tells a careful attacker that this is not a real OpenAI key.

kman cannot mint real provider keys, so this gap cannot be closed. It can be narrowed by planting decoys in places where the kman URL is the *only* plausible endpoint:

- an internal or generic upstream on your own domain (like Tracebit's perimeter sensors)
- an MCP config that points at kman
- a `docker-compose`/`.env` pair where the base URL is clearly required

The docs should say all of this plainly.

---

## 6. Ranked differentiators kman could adopt (solo tier)

Ranked by leak-containment value per unit of effort, for a single operator.

1. **Context-wrapped canary formats beyond `.env`, including an MCP-config decoy.**
   - Prior art: GitGuardian `create-with-context`; Tracebit's `.env`, HAR, Terraform-state and MCP-server decoys; Canarytokens' MCP token.
   - What it would do: `kman canary create --format env|mcp-json|compose|tfvars|shell-rc`. Each format would point at kman URLs, so the decoy is credible and fires.
   - Why it fits: it partly addresses §5. It also catches the newest real threat that Tracebit documents: AI coding agents trying credentials they find on a machine.
   - Effort: low. It is output templating.

2. **A reset-or-revoke lifecycle for canaries, plus known-IP tagging and allowlisting.**
   - Prior art: GitGuardian's event states and IP rules.
   - What it would do:
     - A fired canary moves to `triggered`.
     - `kman canary reset` handles a false alarm, for example when you tripped it yourself.
     - Revoking keeps the history and prompts you to plant a replacement.
     - Named IP tags such as "home" or "ci" annotate alerts and can suppress your own scanners.
   - Why it fits: a solo operator trips their own decoys while testing.
   - Effort: low to medium.

3. **Soft-budget, threshold and projected-spend alerts.**
   - Prior art: LiteLLM's `soft_budget`, `threshold_crossed` at 85/95% and `projected_limit_exceeded`; Portkey's alert thresholds.
   - What it would do: warn before a hard cap blocks the app. keman today only hard-blocks, or alerts on a spike against the baseline.
   - Effort: low. It reuses the spend counters.

4. **"Panic button": bulk revoke or rotate by credential.**
   - Prior art: Vault's `lease revoke -prefix` and its token-tree revocation.
   - What it would do: `kman credential compromised <cred>` would rotate the real key and then rotate or disable every virtual key on that credential, printing the new keys.
   - Why it fits: this is the one-command response when the *real* key leaks, not just a virtual one.
   - Effort: low.

5. **Alert hygiene: throttle per source IP, back off on failing webhooks, and add native Discord, ntfy and Telegram formats.**
   - Prior art: Canarytokens' one alert per IP per minute, its webhook switched off after 5 failures, and its Slack, Discord, Teams and Google Chat formats.
   - What it would do: keman's incident grouping is per key and rule. It would add per-IP throttling for canary floods (cf. GitGuardian's pause at 100 events) and push targets a solo operator actually uses.
   - Effort: low.

6. **Checksummed key format, so `scan` can verify offline.**
   - Prior art: GitHub's partner-program advice of a prefix plus a 32-bit checksum.
   - What it would do: `kmn_<id>_<secret><crc32>`. `scan` could then tell real virtual keys from typos or fixtures with no server and no false positives. With access to the store, it could also say "this one is your canary, don't panic".
   - Why it fits: it is cheap, and it only works if decided before the first release.
   - Effort: low, if done before v1.

7. **Use-count-limited and claim-once keys.**
   - Prior art: Vault's `token_num_uses` and response wrapping; Infisical's `accessTokenNumUsesLimit`.
   - What it would do:
     - `--max-requests N` for one-off jobs and demos.
     - Optionally, a single-use claim link to hand over a new key. A failed claim means it was intercepted, which raises an alert.
   - Effort: medium.

8. **Ceilings on key lifetime and policy.**
   - Prior art: LiteLLM's `upperbound_key_generate_params`; Portkey's maximum TTL per org or workspace.
   - What it would do: a config-level `max_key_ttl` and a cap ceiling, so a hasty `--expires never` is refused rather than allowed.
   - Effort: low.

9. **Temporary limit raise that expires.**
   - Prior art: LiteLLM's `temp_budget_increase` with `temp_budget_expiry`.
   - What it would do: `kman policy bump laptop --daily 20 --for 4h`. It avoids the risky habit of lifting a cap and forgetting it.
   - Effort: low.

10. **Optional scheduled rotation with a file sink.**
    - Prior art: LiteLLM's `auto_rotate`; Portkey's rotation policy; Vault Agent's sinks.
    - Why it ranks last: it only helps if the app re-reads the key from a file kman writes. Solo value is marginal.

**Out of scope for the solo tier** (per `beta.md`), even though competitors have them:

- falling back to another model on budget (Cloudflare Dynamic Routes)
- redacting secrets in prompts (LiteLLM `hide_secrets`, because it needs content inspection)
- becoming a GitHub secret-scanning partner (it needs a public, always-on verify endpoint and a provider identity, which does not fit self-hosting)
- teams and SSO
- email alerts (bridge them through a webhook)

---

## 7. Where keman already beats them

- **Free per-key IP/CIDR and country locks.**
  - LiteLLM: global and Enterprise-only.
  - Portkey, Helicone, Cloudflare and OpenRouter: nothing documented.
  - Infisical and Doppler: paid, and per identity or per config.
  - Vault has CIDR binding but no geography.
- **Behavioural detection with per-key baselines.** keman covers new country and ASN, spend and rate spikes, allowlist probing, and disabled-key use. No gateway surveyed does any anomaly detection; LiteLLM's alerting is fixed thresholds only.
- **Auto-disable with a dry run.** No equivalent was found anywhere.
- **Decoys and enforcement in one tool.** Canary vendors are separate (and mostly SaaS) products, and their self-hosted AWS tokens need cloud infrastructure. Gateways have no decoys at all.
- **Alerts that carry the exact response command.** Competitors link to a dashboard.
- **Free rotation with grace, including `--grace 0`.** LiteLLM charges for rotation (Enterprise), and Portkey's minimum grace is 30 minutes.
- **Non-LLM APIs (Stripe, GitHub, Twilio and others) with mandatory path allowlists.** Every gateway surveyed is LLM-only.
- **Offline `scan --history` that prints the command to move the key behind keman.** Among the tools surveyed, only Infisical and GitGuardian scan, and neither connects a finding to containment.
- **Exact, persisted spend caps.** keman reserves spend before the request and has a fail-closed store. Cloudflare's limits are explicitly approximate, and LiteLLM skips budgets without a database.
- **One binary, no external database.** LiteLLM needs Postgres for virtual keys.

---

## Caveats

- Some claims rest on search summaries of primary pages, marked inline: Portkey rotation, OpenRouter guardrails, Infisical token auth, Doppler Trusted IPs and auto-revoke, the GitHub partner program, and LiteLLM `hide_secrets`. Re-check them before quoting exact field names.
- Helicone's key-limit roadmap comes from a 2023 post. Its current docs page for Vault returned 404.
- Doppler's GitHub auto-revoke comes from a 2020 changelog.
- The Infisical dynamic-secrets overview did not list providers or TTL details.
