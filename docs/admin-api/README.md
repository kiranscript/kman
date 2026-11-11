# kman admin API

Generated from the Go types by `go test ./internal/adminapi -run AdminAPIReferenceIsCurrent -update`; do not edit by hand. CI fails when it is stale. The same document in OpenAPI 3.1 form is [openapi.json](openapi.json), and a running kman serves it at `/admin/v1/openapi.json`, with no token.

The one way to change a running kman (ADR 0004). Every change is one store transaction, audited with its actor: the name of the admin token the request was made with. Generated from the Go types in internal/admin; do not edit by hand.

## Using it

- Every request, except the OpenAPI document, carries `Authorization: Bearer kma_…`, an [admin token](../cli.md#kman-admin-token-create). A cookie is never accepted.
- Request and response bodies are JSON. A request body with a field the API does not know is refused.
- An error is `{"error": {"code": …, "message": …}}` with one of the codes in [Error](#error), and the HTTP status that goes with it (400 `invalid`, 401 `unauthorized`, 404 `not_found`, 409 `conflict`, 500 `internal`).
- Every response carries `Cache-Control: no-store`.
- The listener is `127.0.0.1:8081` by default, or a `0600` Unix socket.

## Operations

| Operation | Summary |
|---|---|
| `GET /admin/v1/admin-tokens` | List every admin token, live or revoked |
| `POST /admin/v1/admin-tokens` | Create a named admin token; its secret is returned only this once |
| `POST /admin/v1/admin-tokens/{name}/revoke` | Revoke an admin token; the install's last live token cannot be revoked |
| `POST /admin/v1/admin-tokens/{name}/rotate` | Give an admin token a new secret; the old one stops working at once |
| `GET /admin/v1/audit` | Read a page of the audit log, oldest entry first |
| `POST /admin/v1/credentials` | Seal a secret as a credential on an upstream; it is never shown again |
| `DELETE /admin/v1/credentials/{ref}` | Delete a credential and wipe its secret; refused while a virtual key that is not revoked depends on it |
| `PATCH /admin/v1/credentials/{ref}` | Rename a credential or replace its secret |
| `POST /admin/v1/credentials/{ref}/compromise` | Replace a leaked credential's secret and rotate every virtual key on it, with no grace, in one step; the new key secrets are returned only this once |
| `GET /admin/v1/exposure` | List the keys that work now and would be easy to abuse: open to every address, never expiring, or unused for 30 days, each with the command that fixes it |
| `GET /admin/v1/keys` | List every virtual key, or the one with a name, never their secrets |
| `POST /admin/v1/keys` | Create a virtual key on an upstream; its secret is returned only this once |
| `GET /admin/v1/keys/{id}` | Read one virtual key, by key id |
| `PATCH /admin/v1/keys/{id}` | Change a virtual key's name, note, tags, expiry or policy |
| `POST /admin/v1/keys/{id}/disable` | Stop a virtual key from the next request until it is enabled again |
| `POST /admin/v1/keys/{id}/enable` | Let a disabled virtual key work again; a revoked key cannot be enabled |
| `POST /admin/v1/keys/{id}/revoke` | End a virtual key for good, from the next request; it cannot be undone |
| `POST /admin/v1/keys/{id}/rotate` | Replace a virtual key's secret and keep the key; the new secret is returned only this once |
| `GET /admin/v1/openapi.json` | This document. It needs no admin token. |
| `DELETE /admin/v1/price-overrides` | Remove a price override |
| `PUT /admin/v1/price-overrides` | Set a price override, install-wide or on one upstream; it wins over the built-in entries until removed |
| `POST /admin/v1/price-overrides/import` | Set many price overrides, all or none |
| `GET /admin/v1/prices` | List the built-in price table, how old it is, and the price overrides |
| `GET /admin/v1/prices/status` | Say how old the built-in price table is, and whether to upgrade |
| `GET /admin/v1/settings` | Read the install's settings |
| `PATCH /admin/v1/settings` | Change some settings; each change is audited |
| `GET /admin/v1/upstreams` | List every upstream with its credentials, never their secrets |
| `POST /admin/v1/upstreams` | Add an upstream, copying a preset or built from the settings |
| `DELETE /admin/v1/upstreams/{ref}` | Delete an upstream, its credentials and its price overrides; refused while a virtual key that is not revoked depends on one |
| `GET /admin/v1/upstreams/{ref}` | Read one upstream, by name or id |
| `PATCH /admin/v1/upstreams/{ref}` | Change some of an upstream's settings |
| `GET /admin/v1/upstreams/{ref}/sync` | Show how an upstream differs from a newer version of its preset, changing nothing |
| `POST /admin/v1/upstreams/{ref}/sync` | Apply a sync plan; refused if the upstream or the preset has moved since |
| `GET /admin/v1/whoami` | Name the admin token this request is made with |

### `GET /admin/v1/admin-tokens`

List every admin token, live or revoked. Operation id `listAdminTokens`.

Response 200: [AdminTokenList](#admintokenlist).

### `POST /admin/v1/admin-tokens`

Create a named admin token; its secret is returned only this once. Operation id `createAdminToken`.

Request body: [CreateAdminTokenRequest](#createadmintokenrequest).

Response 201: [CreatedAdminToken](#createdadmintoken).

### `POST /admin/v1/admin-tokens/{name}/revoke`

Revoke an admin token; the install's last live token cannot be revoked. Operation id `revokeAdminToken`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `name` | path | string | yes | The admin token's name. |

Response 200: [AdminToken](#admintoken).

### `POST /admin/v1/admin-tokens/{name}/rotate`

Give an admin token a new secret; the old one stops working at once. Operation id `rotateAdminToken`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `name` | path | string | yes | The admin token's name. |

Response 200: [CreatedAdminToken](#createdadmintoken).

### `GET /admin/v1/audit`

Read a page of the audit log, oldest entry first. Operation id `listAudit`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `after` | query | integer | no | Return the entries after this id, from the start of the log when 0. |
| `limit` | query | integer | no | The most entries to return, 1 to 1000. Default 100. |
| `latest` | query | boolean | no | Return the newest entries instead. after must then be 0. |

Response 200: [AuditPage](#auditpage).

### `POST /admin/v1/credentials`

Seal a secret as a credential on an upstream; it is never shown again. Operation id `addCredential`.

Request body: [AddCredentialRequest](#addcredentialrequest).

Response 201: [Credential](#credential).

### `DELETE /admin/v1/credentials/{ref}`

Delete a credential and wipe its secret; refused while a virtual key that is not revoked depends on it. Operation id `removeCredential`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The credential's name or id. |

Response 200: [Credential](#credential).

### `PATCH /admin/v1/credentials/{ref}`

Rename a credential or replace its secret. Operation id `updateCredential`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The credential's name or id. |

Request body: [CredentialUpdate](#credentialupdate).

Response 200: [Credential](#credential).

### `POST /admin/v1/credentials/{ref}/compromise`

Replace a leaked credential's secret and rotate every virtual key on it, with no grace, in one step; the new key secrets are returned only this once. Operation id `compromiseCredential`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The credential's name or id. |

Request body: [CompromiseCredentialRequest](#compromisecredentialrequest).

Response 200: [CompromisedCredential](#compromisedcredential).

### `GET /admin/v1/exposure`

List the keys that work now and would be easy to abuse: open to every address, never expiring, or unused for 30 days, each with the command that fixes it. Operation id `getExposure`.

Response 200: [Exposure](#exposure).

### `GET /admin/v1/keys`

List every virtual key, or the one with a name, never their secrets. Operation id `listKeys`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `name` | query | string | no | Only the key not revoked with this name. |

Response 200: [KeyList](#keylist).

### `POST /admin/v1/keys`

Create a virtual key on an upstream; its secret is returned only this once. Operation id `createKey`.

Request body: [CreateKeyRequest](#createkeyrequest).

Response 201: [CreatedKey](#createdkey).

### `GET /admin/v1/keys/{id}`

Read one virtual key, by key id. Operation id `getKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [Key](#key).

### `PATCH /admin/v1/keys/{id}`

Change a virtual key's name, note, tags, expiry or policy. Operation id `updateKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Request body: [KeyUpdate](#keyupdate).

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/disable`

Stop a virtual key from the next request until it is enabled again. Operation id `disableKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/enable`

Let a disabled virtual key work again; a revoked key cannot be enabled. Operation id `enableKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/revoke`

End a virtual key for good, from the next request; it cannot be undone. Operation id `revokeKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/rotate`

Replace a virtual key's secret and keep the key; the new secret is returned only this once. Operation id `rotateKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Request body: [RotateKeyRequest](#rotatekeyrequest).

Response 200: [RotatedKey](#rotatedkey).

### `GET /admin/v1/openapi.json`

This document. It needs no admin token. Operation id `openapi`.

Needs no admin token.

Response 200: object.

### `DELETE /admin/v1/price-overrides`

Remove a price override. Operation id `removePriceOverride`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `upstream` | query | string | no | The upstream it is on, by name or id. Absent for an install-wide override. |
| `model` | query | string | no | The model id, matched exactly. |

Response 200: [PriceOverride](#priceoverride).

### `PUT /admin/v1/price-overrides`

Set a price override, install-wide or on one upstream; it wins over the built-in entries until removed. Operation id `setPriceOverride`.

Request body: [PriceOverrideSet](#priceoverrideset).

Response 200: [PriceOverride](#priceoverride).

### `POST /admin/v1/price-overrides/import`

Set many price overrides, all or none. Operation id `importPriceOverrides`.

Request body: [PriceOverrideImport](#priceoverrideimport).

Response 200: [PriceOverrideImportResult](#priceoverrideimportresult).

### `GET /admin/v1/prices`

List the built-in price table, how old it is, and the price overrides. Operation id `listPrices`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `preset` | query | string | no | Only this preset's entries, or global for the global ones, and the overrides that reach them: install-wide ones and those on an upstream built from it. |
| `model` | query | string | no | Only models whose id contains this, in any case. It narrows the overrides too. |

Response 200: [PriceList](#pricelist).

### `GET /admin/v1/prices/status`

Say how old the built-in price table is, and whether to upgrade. Operation id `getPriceDataStatus`.

Response 200: [PriceDataStatus](#pricedatastatus).

### `GET /admin/v1/settings`

Read the install's settings. Operation id `getSettings`.

Response 200: [Settings](#settings).

### `PATCH /admin/v1/settings`

Change some settings; each change is audited. Operation id `updateSettings`.

Request body: [SettingsUpdate](#settingsupdate).

Response 200: [Settings](#settings).

### `GET /admin/v1/upstreams`

List every upstream with its credentials, never their secrets. Operation id `listUpstreams`.

Response 200: [UpstreamList](#upstreamlist).

### `POST /admin/v1/upstreams`

Add an upstream, copying a preset or built from the settings. Operation id `addUpstream`.

Request body: [AddUpstreamRequest](#addupstreamrequest).

Response 201: [Upstream](#upstream).

### `DELETE /admin/v1/upstreams/{ref}`

Delete an upstream, its credentials and its price overrides; refused while a virtual key that is not revoked depends on one. Operation id `removeUpstream`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The upstream's name or id. |

Response 200: [Upstream](#upstream).

### `GET /admin/v1/upstreams/{ref}`

Read one upstream, by name or id. Operation id `getUpstream`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The upstream's name or id. |

Response 200: [Upstream](#upstream).

### `PATCH /admin/v1/upstreams/{ref}`

Change some of an upstream's settings. Operation id `updateUpstream`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The upstream's name or id. |

Request body: [UpstreamUpdate](#upstreamupdate).

Response 200: [Upstream](#upstream).

### `GET /admin/v1/upstreams/{ref}/sync`

Show how an upstream differs from a newer version of its preset, changing nothing. Operation id `planUpstreamSync`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The upstream's name or id. |

Response 200: [UpstreamSyncPlan](#upstreamsyncplan).

### `POST /admin/v1/upstreams/{ref}/sync`

Apply a sync plan; refused if the upstream or the preset has moved since. Operation id `syncUpstream`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The upstream's name or id. |

Request body: [UpstreamSyncRequest](#upstreamsyncrequest).

Response 200: [Upstream](#upstream).

### `GET /admin/v1/whoami`

Name the admin token this request is made with. Operation id `whoami`.

Response 200: [Whoami](#whoami).

## Schemas

### `AddCredentialRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among credentials not deleted. |
| `secret` | string | yes | The real secret. kman seals it and never shows it again. For basic auth without a username, the whole user:password. |
| `upstream` | string | yes | The upstream's name or id. |

### `AddUpstreamRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `auth` | [Auth](#auth) | no | A custom upstream defaults to Authorization: Bearer {key}, or x-api-key: {key} on the anthropic profile. |
| `credential` | [NewCredential](#newcredential) | no | A first credential, added in the same transaction: a bad secret adds no upstream either. |
| `host` | string | no | A host name or IP address, with no scheme, port or path. A custom upstream needs one. |
| `inject_usage` | boolean | no | Only for the openai profile. |
| `name` | string | no | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit. With a preset, it defaults to the preset's name. |
| `path_allowlist` | list of string | no | The rules a request must match, as METHOD /path. A custom upstream needs at least one; * /** allows everything. |
| `port` | integer | no | A custom upstream defaults to 443 with TLS and 80 without. |
| `preset` | string | no | The preset to copy. Absent to build a custom upstream from the settings. |
| `price_multiplier_ppm` | integer | no | What built-in prices and install-wide overrides are multiplied by on this upstream, in parts per million, for a regional uplift: 1000000 is x1, the default, and 1100000 is x1.1. Above 0, up to x100. It does not touch a per-upstream override or a cost the provider reports. |
| `profile` | string | no | openai, anthropic or generic. A custom upstream defaults to generic. |
| `sdk_base_path` | string | no |  |
| `sdk_env` | string | no |  |
| `server_name` | string | no | The TLS server name, when it is not the host. |
| `tls` | boolean | no | A custom upstream defaults to true. |

### `AdminToken`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `last_used_at` | string | no | When the token last authenticated a request, to the minute. Absent if never. |
| `name` | string | yes | The token's name, which is the actor of every change made with it. |
| `revoked_at` | string | no | When the token was revoked. Absent while it is live. |

### `AdminTokenList`

| Field | Type | Required | Description |
|---|---|---|---|
| `tokens` | list of [AdminToken](#admintoken) | yes |  |

### `AuditEntry`

| Field | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes | Such as admin_token.create or settings.set. |
| `actor` | string | yes | Who made the change: an admin token's name, or system:<what> for kman itself. |
| `after` |  | no | The target after the change, where the action records it. Never a secret. |
| `before` |  | no | The target before the change, where the action records it. Never a secret. |
| `client_ip` | string | no |  |
| `id` | integer | yes | Rising with each entry; the cursor for paging. |
| `incident_id` | integer | no |  |
| `target_id` | string | yes |  |
| `target_kind` | string | yes |  |
| `time` | string | yes |  |

### `AuditPage`

| Field | Type | Required | Description |
|---|---|---|---|
| `entries` | list of [AuditEntry](#auditentry) | yes |  |
| `more` | boolean | yes | Whether the log holds more entries beyond this page: after it, or before it with latest. |

### `Auth`

| Field | Type | Required | Description |
|---|---|---|---|
| `basic_username` | string | no | For the basic style, the username. When absent, a credential's secret is the whole user:password. |
| `header` | string | no | For the header style, the template: Name: value {key}. |
| `style` | string | yes | header or basic. |

### `CompromiseCredentialRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `secret` | string | yes | The new real secret, made at the provider after the leaked one was revoked there. kman seals it and never shows it again. |

### `CompromisedCredential`

| Field | Type | Required | Description |
|---|---|---|---|
| `credential` | [Credential](#credential) | yes |  |
| `rotated_keys` | list of [RotatedKey](#rotatedkey) | yes | Every virtual key on the credential that was not revoked, oldest first, with its new secret, shown only this once. Each is an app that needs its new key. |

### `CreateAdminTokenRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit. A revoked token's name stays taken. |

### `CreateKeyRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `credential` | string | no | The credential's name or id. Needed when the upstream has more than one. |
| `name` | string | no | An optional label: 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among keys not revoked. |
| `note` | string | no | Free text of at most 1024 bytes, with no control characters. |
| `policy` |  | no | The key's policy, a JSON object. Defaults to the empty policy. |
| `tags` | list of string | no | At most 32 labels of a-z, 0-9, '.', '_', ':' and '-'. |
| `ttl_seconds` | integer | no | How long the key stays valid from now, at most the maximum key lifetime. Defaults to the default key TTL. |
| `upstream` | string | yes | The upstream's name or id. |

### `CreatedAdminToken`

| Field | Type | Required | Description |
|---|---|---|---|
| `secret` | string | yes | The token itself (kma_...). kman keeps only its hash, so it is shown only this once. |
| `token` | [AdminToken](#admintoken) | yes |  |

### `CreatedKey`

| Field | Type | Required | Description |
|---|---|---|---|
| `key` | [Key](#key) | yes |  |
| `sdk_base_path` | string | no | Appended to kman's proxy URL to make an SDK's base URL. |
| `sdk_env` | string | no | The variable an SDK for its upstream reads the key from. |
| `secret` | string | yes | The virtual key itself (kmn_...). kman keeps only its hash, so it is shown only this once. |

### `Credential`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `id` | string | yes |  |
| `last4` | string | no | The end of the secret, to tell credentials apart. Absent for a secret too short to show any of. |
| `name` | string | yes |  |
| `rotated_at` | string | no | When the secret was last replaced. |
| `upstream` | string | yes | The upstream's name. |
| `upstream_id` | string | yes |  |

### `CredentialUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | A new name. |
| `secret` | string | no | A new secret, which overwrites the old one. The credential keeps its id, so virtual keys on it are not touched. |

### `Error`

| Field | Type | Required | Description |
|---|---|---|---|
| `code` | one of `invalid`, `not_found`, `conflict`, `unauthorized`, `internal` | yes |  |
| `message` | string | yes |  |

### `ErrorResponse`

| Field | Type | Required | Description |
|---|---|---|---|
| `error` | [Error](#error) | yes |  |

### `Exposure`

| Field | Type | Required | Description |
|---|---|---|---|
| `findings` | list of [ExposureFinding](#exposurefinding) | yes |  |
| `keys_checked` | integer | yes | How many keys were looked at: the keys that work now. A disabled, revoked or expired key cannot be abused, and a canary is meant to be left alone. |

### `ExposureFinding`

| Field | Type | Required | Description |
|---|---|---|---|
| `fix` | string | yes | The kman command that fixes it, ready to run. An open key's has a placeholder to replace with the network the key is used from. |
| `key_id` | string | yes |  |
| `last_used_at` | string | no | For unused: the start of the last hour in which the key forwarded a request. Absent if it never has. |
| `message` | string | yes |  |
| `name` | string | no |  |
| `rule` | string | yes | open_to_every_address, never_expires or unused. |

### `Key`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `credential` | string | no | The credential's name. Absent once a revoked key's credential is deleted. |
| `credential_id` | string | no |  |
| `expired` | boolean | yes | Whether its expiry has passed, whatever its status. |
| `expires_at` | string | yes | When the key stops working. Every key has one: there is no never. |
| `id` | string | yes | The key id: the non-secret handle inside the key, and how this API addresses it. |
| `name` | string | no | An optional label, unique among keys not revoked. |
| `note` | string | no |  |
| `policy` |  | yes | The key's policy, a JSON object: empty until it is edited. |
| `policy_version` | integer | yes | The version of the policy format the policy is in. |
| `spend` | [KeySpend](#keyspend) | no | What the key has spent today and this month, beside its caps. Shown when the key is read, and not in the audit log. |
| `status` | string | yes | active, disabled or revoked. |
| `tags` | list of string | yes |  |
| `upstream` | string | no | The upstream's name. Absent once a revoked key's upstream is deleted. |
| `upstream_id` | string | no |  |

### `KeyList`

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | list of [Key](#key) | yes |  |

### `KeySpend`

| Field | Type | Required | Description |
|---|---|---|---|
| `month` | [SpendPeriod](#spendperiod) | yes |  |
| `today` | [SpendPeriod](#spendperiod) | yes |  |

### `KeyUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | A new name, or empty for none. |
| `note` | string | no | A new note, or empty for none. |
| `policy` |  | no | The new policy, a JSON object, which replaces the old one. |
| `tags` | list of string | no | The new tags, which replace the old ones. |
| `ttl_seconds` | integer | no | A new expiry, this long from now. It may not be later than the key's creation plus the maximum key lifetime, unless that shortens the key's life. |

### `NewCredential`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | Defaults to the upstream's name. |
| `secret` | string | yes | The real secret. kman seals it and never shows it again. For basic auth without a username, the whole user:password. |

### `PriceBand`

| Field | Type | Required | Description |
|---|---|---|---|
| `above_tokens` | integer | yes |  |
| `input_micro` | integer | yes |  |
| `output_micro` | integer | yes |  |

### `PriceDataStatus`

| Field | Type | Required | Description |
|---|---|---|---|
| `age_days` | integer | yes |  |
| `age_seconds` | integer | yes |  |
| `generated_at` | string | yes |  |
| `notice` | string | no | What to tell the operator when it is stale. |
| `stale` | boolean | yes | Whether the table is past 60 days old. |

### `PriceList`

| Field | Type | Required | Description |
|---|---|---|---|
| `models` | list of [PriceModel](#pricemodel) | yes | The built-in entries. An override below may hide one. |
| `overrides` | list of [PriceOverride](#priceoverride) | yes | The price overrides, which win over the built-in entries until removed. |
| `status` | [PriceDataStatus](#pricedatastatus) | yes |  |

### `PriceModel`

| Field | Type | Required | Description |
|---|---|---|---|
| `audio_input_micro` | integer | no | Audio input. Absent: billed at the input price. |
| `audio_output_micro` | integer | no | Audio output. Absent: billed at the output price. |
| `cache_write_micro` | integer | no | Cache writes. Absent: billed at the input price. |
| `cached_micro` | integer | no | Cached input. Absent: billed at the input price. |
| `context_window` | integer | no |  |
| `input_micro` | integer | yes |  |
| `long_context` | list of [PriceBand](#priceband) | no | Prices for a whole request whose input is over a size. |
| `max_output` | integer | yes | The most tokens one response can output, which sizes reservations. |
| `mode` | string | yes | chat, responses, completion, embedding or realtime. |
| `model` | string | yes | The model id, matched exactly. |
| `output_micro` | integer | yes |  |
| `per_minute_micro` | integer | no | Voice: per minute, billed per second. |
| `per_text_input_micro` | integer | no | Voice: per text input. |
| `preset` | string | no | The preset this entry belongs to. Absent for a global entry, which every upstream falls back to. |
| `reviewed` | boolean | yes | Whether a person checked max_output against the provider's docs. |
| `service_tiers` | list of string | no | The service tiers with their own prices. |
| `tool_fees` | list of [PriceToolFee](#pricetoolfee) | no | Server-tool fees, per call. |

### `PriceOverride`

| Field | Type | Required | Description |
|---|---|---|---|
| `actor` | string | yes | Who last set it. |
| `audio_cached_micro` | integer | no | Cached audio input. Absent: billed at the audio input price. |
| `audio_input_micro` | integer | no | Audio input. Absent: billed at the input price. |
| `audio_output_micro` | integer | no | Audio output. Absent: billed at the output price. |
| `cache_write_1h_micro` | integer | no | One-hour cache writes. Absent: billed at the cache write price. |
| `cache_write_micro` | integer | no | Cache writes. Absent: billed at the input price. |
| `cached_micro` | integer | no | Cached input. Absent: billed at the input price. |
| `hides` | list of string | no | The sections of the price table whose built-in entry for this model it hides: global or a preset's name. |
| `input_micro` | integer | yes |  |
| `max_output` | integer | yes | The most tokens one response can output, which sizes reservations. |
| `model` | string | yes | The model id, matched exactly. |
| `output_micro` | integer | yes |  |
| `updated_at` | string | yes |  |
| `upstream` | string | no | The upstream it applies to, by name. Absent for an install-wide override. |
| `upstream_id` | string | no |  |

### `PriceOverrideImport`

| Field | Type | Required | Description |
|---|---|---|---|
| `overrides` | list of [PriceOverrideSet](#priceoverrideset) | yes |  |

### `PriceOverrideImportResult`

| Field | Type | Required | Description |
|---|---|---|---|
| `changed` | integer | yes | How many it added or changed, each with its own audit entry. |
| `overrides` | list of [PriceOverride](#priceoverride) | yes | Every override it named, as it now stands. |
| `unchanged` | integer | yes | How many already had these prices. |

### `PriceOverrideSet`

| Field | Type | Required | Description |
|---|---|---|---|
| `audio_cached_micro` | integer | no | Cached audio input. Absent: billed at the audio input price. |
| `audio_input_micro` | integer | no | Audio input. Absent: billed at the input price. |
| `audio_output_micro` | integer | no | Audio output. Absent: billed at the output price. |
| `cache_write_1h_micro` | integer | no | One-hour cache writes. Absent: billed at the cache write price. |
| `cache_write_micro` | integer | no | Cache writes. Absent: billed at the input price. |
| `cached_micro` | integer | no | Cached input. Absent: billed at the input price. |
| `input_micro` | integer | yes | Input, in microdollars per million tokens. |
| `max_output` | integer | yes | The most tokens one response can output, which sizes reservations. |
| `model` | string | yes | The model id, matched exactly. |
| `output_micro` | integer | yes | Output, in microdollars per million tokens. |
| `upstream` | string | no | The upstream to price it on, by name or id. Absent for the whole install. |

### `PriceToolFee`

| Field | Type | Required | Description |
|---|---|---|---|
| `micro` | integer | yes |  |
| `tool` | string | yes |  |

### `RotateKeyRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `grace_seconds` | integer | no | How long the old secret keeps working beside the new one, at most 30 days. Absent or 0 ends it at once, on the next request. |

### `RotatedKey`

| Field | Type | Required | Description |
|---|---|---|---|
| `key` | [Key](#key) | yes | The same key, with the same id, spend, policy and history: only its secret is new. |
| `previous_valid_until` | string | no | When the old secret stops working. Absent when it stopped at once. |
| `sdk_env` | string | no | The variable an SDK for its upstream reads the key from. |
| `secret` | string | yes | The new virtual key (kmn_...). kman keeps only its hash, so it is shown only this once. |

### `Settings`

| Field | Type | Required | Description |
|---|---|---|---|
| `default_key_ttl_seconds` | integer | yes | How long a new virtual key stays valid unless told otherwise. At most the maximum key lifetime. Default 90 days. |
| `max_key_lifetime_seconds` | integer | yes | The longest any virtual key may stay valid. No key is created or extended past it. Default 365 days. |
| `soft_budget_defaults` | list of integer | yes | The percentages of each spend cap that warn when crossed, rising, for keys that set none. Default 50, 80, 100. |
| `spend_timezone` | string | yes | The IANA time zone in which daily and monthly spend periods start at 00:00. Default UTC. A change takes effect when kman restarts. |

### `SettingsUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `default_key_ttl_seconds` | integer | no |  |
| `max_key_lifetime_seconds` | integer | no |  |
| `soft_budget_defaults` | list of integer | no |  |
| `spend_timezone` | string | no |  |

### `SpendPeriod`

| Field | Type | Required | Description |
|---|---|---|---|
| `cap_micro` | integer | no | The key's cap for the period, in microdollars. Absent for none. |
| `cost_micro` | integer | yes | What has been charged, in microdollars, estimated_micro included. |
| `estimated_micro` | integer | yes | The part of cost_micro charged as an estimate: the whole reservation, for a response that was cut off or reported no usage. |
| `period` | string | yes | The day, such as 2026-11-11, or the month, such as 2026-11. |
| `requests` | integer | yes | Requests charged. |
| `reserved_micro` | integer | yes | Held for requests in flight. A cap counts it as it counts cost_micro. |
| `unmetered_requests` | integer | yes | Requests forwarded that could not be metered, whose cost is unknown and so is not in cost_micro. |

### `Upstream`

| Field | Type | Required | Description |
|---|---|---|---|
| `auth` | [Auth](#auth) | yes |  |
| `created_at` | string | yes |  |
| `credentials` | list of [Credential](#credential) | no | Its credentials, by name. Never a secret. |
| `host` | string | yes |  |
| `id` | string | yes |  |
| `inject_usage` | boolean | yes | Whether OpenAI-style streams get stream_options.include_usage. |
| `name` | string | yes |  |
| `path_allowlist` | list of string | yes | The rules a request must match, as METHOD /path: * is any method or one segment, and a last segment of ** is the rest of the path. |
| `port` | integer | yes |  |
| `preset` | string | no | The preset it was copied from. Absent for a custom upstream. |
| `preset_version` | integer | no | The version of the preset it was last copied from. |
| `price_multiplier_ppm` | integer | yes | What built-in prices and install-wide overrides are multiplied by on this upstream, in parts per million: 1000000 is x1. |
| `profile` | string | yes | openai, anthropic or generic. |
| `sdk_base_path` | string | no | Appended to kman's proxy URL to make an SDK's base URL. |
| `sdk_env` | string | no | The variable an SDK for this upstream reads its key from. |
| `server_name` | string | no | The TLS server name, when it is not the host. |
| `tls` | boolean | yes |  |
| `update_available` | boolean | no | Whether this kman holds a newer version of the preset: see the sync operation. |
| `updated_at` | string | yes |  |

### `UpstreamChange`

| Field | Type | Required | Description |
|---|---|---|---|
| `field` | string | yes | The upstream's field, by its name in the API. |
| `from` | string | no | What it holds now. Absent if nothing. |
| `to` | string | no | What the sync would set. Absent if nothing. |

### `UpstreamList`

| Field | Type | Required | Description |
|---|---|---|---|
| `upstreams` | list of [Upstream](#upstream) | yes |  |

### `UpstreamSyncPlan`

| Field | Type | Required | Description |
|---|---|---|---|
| `changes` | list of [UpstreamChange](#upstreamchange) | yes | What the sync would change, which undoes any edit made since the last copy. Empty when up to date, and possibly when only the version moves. |
| `from_version` | integer | yes | The version of the preset the upstream was last copied from. |
| `generation` | integer | yes | The upstream's generation: pass it to the sync, which refuses if the upstream has changed since. |
| `preset` | string | yes |  |
| `to_version` | integer | yes | The version of the preset this kman holds. |
| `up_to_date` | boolean | yes | Whether this kman holds no newer version, so there is nothing to sync. |
| `upstream` | string | yes |  |

### `UpstreamSyncRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `generation` | integer | yes | The plan's generation. |
| `to_version` | integer | yes | The plan's to_version. |

### `UpstreamUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `auth` | [Auth](#auth) | no | A custom upstream defaults to Authorization: Bearer {key}, or x-api-key: {key} on the anthropic profile. |
| `host` | string | no | A host name or IP address, with no scheme, port or path. A custom upstream needs one. |
| `inject_usage` | boolean | no | Only for the openai profile. |
| `name` | string | no | A new name. |
| `path_allowlist` | list of string | no | The rules a request must match, as METHOD /path. A custom upstream needs at least one; * /** allows everything. |
| `port` | integer | no | A custom upstream defaults to 443 with TLS and 80 without. |
| `price_multiplier_ppm` | integer | no | What built-in prices and install-wide overrides are multiplied by on this upstream, in parts per million, for a regional uplift: 1000000 is x1, the default, and 1100000 is x1.1. Above 0, up to x100. It does not touch a per-upstream override or a cost the provider reports. |
| `profile` | string | no | openai, anthropic or generic. A custom upstream defaults to generic. |
| `sdk_base_path` | string | no |  |
| `sdk_env` | string | no |  |
| `server_name` | string | no | The TLS server name, when it is not the host. |
| `tls` | boolean | no | A custom upstream defaults to true. |

### `Whoami`

| Field | Type | Required | Description |
|---|---|---|---|
| `actor` | string | yes | The name of the admin token the request was made with. |
