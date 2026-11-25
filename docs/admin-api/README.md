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
| `POST /admin/v1/backup` | Download a consistent copy of the database, made while kman runs; it holds ciphertext only, so it opens only with the master key |
| `GET /admin/v1/canaries` | List every canary, with where it was planted, whether it has fired and when it last did, and where its last use came from; the address is left out unless the expose_client_addresses setting is on |
| `POST /admin/v1/canaries` | Make a canary: a key in the standard format with no credential, which forwards nothing and raises a critical alert when used. Its secret and the snippet to plant are returned only this once |
| `GET /admin/v1/canaries/{id}` | Read one canary, by key id |
| `PATCH /admin/v1/canaries/{id}` | Change where a canary was planted, its tags and its ignore list |
| `POST /admin/v1/canaries/{id}/reset` | Arm a fired canary again, so its next use alerts; what it recorded stays |
| `GET /admin/v1/channels` | List the alert channels, with what each one's outbox holds, never their addresses |
| `POST /admin/v1/channels` | Add a Slack or signed-webhook alert channel; a webhook's signing secret is returned only this once |
| `DELETE /admin/v1/channels/{ref}` | Remove an alert channel and give up the alerts still waiting for it |
| `POST /admin/v1/channels/{ref}/test` | Send a test alert to a channel now and say whether it arrived |
| `POST /admin/v1/credentials` | Seal a secret as a credential on an upstream; it is never shown again |
| `DELETE /admin/v1/credentials/{ref}` | Delete a credential and wipe its secret; refused while a virtual key that is not revoked depends on it |
| `PATCH /admin/v1/credentials/{ref}` | Rename a credential or replace its secret |
| `POST /admin/v1/credentials/{ref}/compromise` | Replace a leaked credential's secret and rotate every virtual key on it, with no grace, in one step; the new key secrets are returned only this once |
| `GET /admin/v1/events` | List stored events, newest first: which key, from where, by what client, what operation, the status and any refusal code, never content; filtered by key, kind, refusal code and time, and paged. A client address is left out unless the expose_client_addresses setting is on |
| `GET /admin/v1/events/stream` | Follow new events as they happen, as server-sent events, with the filters of the list. A client that cannot keep up loses events and is told how many |
| `GET /admin/v1/exposure` | List the keys that work now and would be easy to abuse: open to every address, never expiring, or unused for 30 days, each with the command that fixes it |
| `GET /admin/v1/incidents` | List incidents, newest first: the open case for each key whose rules fired, with its state, severity, rules and how many detections it has; filtered by open, and key, and paged |
| `GET /admin/v1/incidents/{id}` | Read one incident with its detections and the actions taken on it, which are its timeline, and the places it saw that this was me could add as known sources. A client address or network in the evidence is left out unless the expose_client_addresses setting is on |
| `POST /admin/v1/incidents/{id}/acknowledge` | Acknowledge an incident: its repeats stop alerting, and an escalation still alerts |
| `POST /admin/v1/incidents/{id}/resolve` | Resolve an incident as confirmed (the default) or false_positive |
| `POST /admin/v1/incidents/{id}/this-was-me` | Resolve an incident as a false positive and, if asked, add the places it saw as known sources, for its key or for every key. Never for a canary |
| `GET /admin/v1/keys` | List every virtual key, or the one with a name, never their secrets |
| `POST /admin/v1/keys` | Create a virtual key on an upstream; its secret is returned only this once |
| `GET /admin/v1/keys/{id}` | Read one virtual key, by key id |
| `PATCH /admin/v1/keys/{id}` | Change a virtual key's name, note, tags, expiry or policy |
| `GET /admin/v1/keys/{id}/clients` | List the client addresses a virtual key was used from, grouped, from the events kept; refused unless the expose_client_addresses setting is on |
| `POST /admin/v1/keys/{id}/disable` | Stop a virtual key from the next request until it is enabled again |
| `POST /admin/v1/keys/{id}/enable` | Let a disabled virtual key work again; a revoked key cannot be enabled |
| `GET /admin/v1/keys/{id}/places` | List where a virtual key has been used from: each country, AS and network, and each client (the User-Agent reduced to a product and major version) that used it, with when it was first and last seen and how many requests were forwarded and refused |
| `POST /admin/v1/keys/{id}/revoke` | End a virtual key for good, from the next request; it cannot be undone |
| `POST /admin/v1/keys/{id}/rotate` | Replace a virtual key's secret and keep the key; the new secret is returned only this once |
| `GET /admin/v1/known-sources` | List the known sources: labelled networks, countries, ASNs and clients the operator says are legitimate, for every key or one. They never silence a canary |
| `POST /admin/v1/known-sources` | Add a known source: a labelled CIDR, country, ASN or client, for every key or one |
| `DELETE /admin/v1/known-sources/{id}` | Remove a known source |
| `GET /admin/v1/openapi.json` | This document. It needs no admin token. |
| `DELETE /admin/v1/price-overrides` | Remove a price override |
| `PUT /admin/v1/price-overrides` | Set a price override, install-wide or on one upstream; it wins over the built-in entries until removed |
| `POST /admin/v1/price-overrides/import` | Set many price overrides, all or none |
| `GET /admin/v1/prices` | List the built-in price table, how old it is, and the price overrides |
| `GET /admin/v1/prices/status` | Say how old the built-in price table is, and whether to upgrade |
| `POST /admin/v1/prune` | Delete events, hourly usage and delivered alerts older than a date, and with audit_log the audit log's entries too; the prune is audited |
| `GET /admin/v1/settings` | Read the install's settings |
| `PATCH /admin/v1/settings` | Change some settings; each change is audited |
| `GET /admin/v1/update` | Say whether a newer kman release is known; only an install with update_check on ever asks |
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

### `POST /admin/v1/backup`

Download a consistent copy of the database, made while kman runs; it holds ciphertext only, so it opens only with the master key. Operation id `createBackup`.

Response 200: a file (application/octet-stream).

### `GET /admin/v1/canaries`

List every canary, with where it was planted, whether it has fired and when it last did, and where its last use came from; the address is left out unless the expose_client_addresses setting is on. Operation id `listCanaries`.

Response 200: [CanaryList](#canarylist).

### `POST /admin/v1/canaries`

Make a canary: a key in the standard format with no credential, which forwards nothing and raises a critical alert when used. Its secret and the snippet to plant are returned only this once. Operation id `createCanary`.

Request body: [CreateCanaryRequest](#createcanaryrequest).

Response 201: [CreatedCanary](#createdcanary).

### `GET /admin/v1/canaries/{id}`

Read one canary, by key id. Operation id `getCanary`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The canary's key id. |

Response 200: [Canary](#canary).

### `PATCH /admin/v1/canaries/{id}`

Change where a canary was planted, its tags and its ignore list. Operation id `updateCanary`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The canary's key id. |

Request body: [CanaryUpdate](#canaryupdate).

Response 200: [Canary](#canary).

### `POST /admin/v1/canaries/{id}/reset`

Arm a fired canary again, so its next use alerts; what it recorded stays. Operation id `resetCanary`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The canary's key id. |

Response 200: [Canary](#canary).

### `GET /admin/v1/channels`

List the alert channels, with what each one's outbox holds, never their addresses. Operation id `listChannels`.

Response 200: [ChannelList](#channellist).

### `POST /admin/v1/channels`

Add a Slack or signed-webhook alert channel; a webhook's signing secret is returned only this once. Operation id `addChannel`.

Request body: [AddChannelRequest](#addchannelrequest).

Response 201: [CreatedChannel](#createdchannel).

### `DELETE /admin/v1/channels/{ref}`

Remove an alert channel and give up the alerts still waiting for it. Operation id `removeChannel`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The channel's name or id. |

Response 200: [Channel](#channel).

### `POST /admin/v1/channels/{ref}/test`

Send a test alert to a channel now and say whether it arrived. Operation id `testChannel`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `ref` | path | string | yes | The channel's name or id. |

Response 200: [ChannelTestResult](#channeltestresult).

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

### `GET /admin/v1/events`

List stored events, newest first: which key, from where, by what client, what operation, the status and any refusal code, never content; filtered by key, kind, refusal code and time, and paged. A client address is left out unless the expose_client_addresses setting is on. Operation id `listEvents`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `key` | query | string | no | Only events of this key id. |
| `kind` | query | string | no | Only events of this kind: forwarded or refused. |
| `code` | query | string | no | Only events with this refusal code, such as invalid_key. |
| `since` | query | string | no | Only events at or after this time (RFC 3339). |
| `until` | query | string | no | Only events before this time (RFC 3339). |
| `before` | query | integer | no | Return the events older than the one with this id: the previous page's next_before. From the newest when 0. |
| `limit` | query | integer | no | The most events to return, 1 to 1000. Default 100. |

Response 200: [EventPage](#eventpage).

### `GET /admin/v1/events/stream`

Follow new events as they happen, as server-sent events, with the filters of the list. A client that cannot keep up loses events and is told how many. Operation id `streamEvents`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `key` | query | string | no | Only events of this key id. |
| `kind` | query | string | no | Only events of this kind: forwarded or refused. |
| `code` | query | string | no | Only events with this refusal code, such as invalid_key. |

Response 200: a stream of server-sent events (text/event-stream), each message's data one [Event](#event).

### `GET /admin/v1/exposure`

List the keys that work now and would be easy to abuse: open to every address, never expiring, or unused for 30 days, each with the command that fixes it. Operation id `getExposure`.

Response 200: [Exposure](#exposure).

### `GET /admin/v1/incidents`

List incidents, newest first: the open case for each key whose rules fired, with its state, severity, rules and how many detections it has; filtered by open, and key, and paged. Operation id `listIncidents`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `open` | query | boolean | no | Only incidents that are not resolved. |
| `key` | query | string | no | Only this key's incidents, by key id. |
| `before` | query | integer | no | Only incidents with an id below this: the cursor for paging. |
| `limit` | query | integer | no | The most incidents to return, 1 to 500. Default 50. |

Response 200: [IncidentList](#incidentlist).

### `GET /admin/v1/incidents/{id}`

Read one incident with its detections and the actions taken on it, which are its timeline, and the places it saw that this was me could add as known sources. A client address or network in the evidence is left out unless the expose_client_addresses setting is on. Operation id `getIncident`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | integer | yes | The incident's id. |

Response 200: [IncidentDetail](#incidentdetail).

### `POST /admin/v1/incidents/{id}/acknowledge`

Acknowledge an incident: its repeats stop alerting, and an escalation still alerts. Operation id `acknowledgeIncident`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | integer | yes | The incident's id. |

Response 200: [Incident](#incident).

### `POST /admin/v1/incidents/{id}/resolve`

Resolve an incident as confirmed (the default) or false_positive. Operation id `resolveIncident`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | integer | yes | The incident's id. |

Request body: [ResolveIncidentRequest](#resolveincidentrequest).

Response 200: [Incident](#incident).

### `POST /admin/v1/incidents/{id}/this-was-me`

Resolve an incident as a false positive and, if asked, add the places it saw as known sources, for its key or for every key. Never for a canary. Operation id `thisWasMe`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | integer | yes | The incident's id. |

Request body: [ThisWasMeRequest](#thiswasmerequest).

Response 200: [ThisWasMeResult](#thiswasmeresult).

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

### `GET /admin/v1/keys/{id}/clients`

List the client addresses a virtual key was used from, grouped, from the events kept; refused unless the expose_client_addresses setting is on. Operation id `getKeyClients`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [KeyClients](#keyclients).

### `POST /admin/v1/keys/{id}/disable`

Stop a virtual key from the next request until it is enabled again. Operation id `disableKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Request body: [ContainKeyRequest](#containkeyrequest).

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/enable`

Let a disabled virtual key work again; a revoked key cannot be enabled. Operation id `enableKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Response 200: [Key](#key).

### `GET /admin/v1/keys/{id}/places`

List where a virtual key has been used from: each country, AS and network, and each client (the User-Agent reduced to a product and major version) that used it, with when it was first and last seen and how many requests were forwarded and refused. Operation id `listKeyPlaces`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |
| `kind` | query | string | no | Only places of this kind: country, asn, network or client. |

Response 200: [KeyPlaces](#keyplaces).

### `POST /admin/v1/keys/{id}/revoke`

End a virtual key for good, from the next request; it cannot be undone. Operation id `revokeKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Request body: [ContainKeyRequest](#containkeyrequest).

Response 200: [Key](#key).

### `POST /admin/v1/keys/{id}/rotate`

Replace a virtual key's secret and keep the key; the new secret is returned only this once. Operation id `rotateKey`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | string | yes | The key id. |

Request body: [RotateKeyRequest](#rotatekeyrequest).

Response 200: [RotatedKey](#rotatedkey).

### `GET /admin/v1/known-sources`

List the known sources: labelled networks, countries, ASNs and clients the operator says are legitimate, for every key or one. They never silence a canary. Operation id `listKnownSources`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `key` | query | string | no | Only those that apply to this key, by key id: the install-wide ones and its own. |

Response 200: [KnownSourceList](#knownsourcelist).

### `POST /admin/v1/known-sources`

Add a known source: a labelled CIDR, country, ASN or client, for every key or one. Operation id `addKnownSource`.

Request body: [AddKnownSourceRequest](#addknownsourcerequest).

Response 201: [KnownSource](#knownsource).

### `DELETE /admin/v1/known-sources/{id}`

Remove a known source. Operation id `removeKnownSource`.

| Parameter | In | Type | Required | Description |
|---|---|---|---|---|
| `id` | path | integer | yes | The known source's id. |

Response 200: [KnownSource](#knownsource).

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

### `POST /admin/v1/prune`

Delete events, hourly usage and delivered alerts older than a date, and with audit_log the audit log's entries too; the prune is audited. Operation id `pruneData`.

Request body: [PruneRequest](#prunerequest).

Response 200: [PruneResult](#pruneresult).

### `GET /admin/v1/settings`

Read the install's settings. Operation id `getSettings`.

Response 200: [Settings](#settings).

### `PATCH /admin/v1/settings`

Change some settings; each change is audited. Operation id `updateSettings`.

Request body: [SettingsUpdate](#settingsupdate).

Response 200: [Settings](#settings).

### `GET /admin/v1/update`

Say whether a newer kman release is known; only an install with update_check on ever asks. Operation id `getUpdateStatus`.

Response 200: [UpdateStatus](#updatestatus).

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

### `AddChannelRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | yes | slack or webhook. |
| `lifecycle_alerts` | boolean | no | Whether the channel also takes spend and lifecycle warnings. Default true. |
| `min_severity` | string | no | The lowest severity the channel takes: low, medium, high or critical. Default medium. |
| `name` | string | yes | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among channels not removed. |
| `url` | string | yes | Where alerts go: a Slack incoming-webhook address, or the webhook's. It is https, or http to this machine. kman seals it and never shows it again. |

### `AddCredentialRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among credentials not deleted. |
| `secret` | string | yes | The real secret. kman seals it and never shows it again. For basic auth without a username, the whole user:password. |
| `upstream` | string | yes | The upstream's name or id. |

### `AddKnownSourceRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `key` | string | no | The key id it applies to. Absent: every key. A canary has none. |
| `kind` | string | yes | cidr, country, asn or client. |
| `label` | string | yes | What it is, 1 to 64 characters with no control characters. |
| `value` | string | yes | cidr: a network no wider than an IPv4 /16 or an IPv6 /32, or one address. country: two capital letters. asn: a number, with or without AS. client: a product and major version as events record it, such as curl/8; none and unknown are refused, since they would silence every request that does not say what it is. |

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

### `Canary`

| Field | Type | Required | Description |
|---|---|---|---|
| `armed_at` | string | yes | When it was made or last reset. |
| `created_at` | string | yes |  |
| `fired_at` | string | no | The first use since it was armed. Absent while it is armed. |
| `fires` | integer | yes | How many uses were counted since the canary was made, from addresses not on its ignore list. |
| `id` | string | yes | The key id: the non-secret handle inside the canary's key, and how this API addresses it. It is the id the canary's alerts and its events name. |
| `ignore` | list of string | yes | Addresses and networks whose use does not fire it, such as the operator's own scanners. The use is still an event. |
| `last_fired_at` | string | no | The last use. A reset keeps it. |
| `last_use` | [CanaryOrigin](#canaryorigin) | no | Where the last use came from. |
| `name` | string | no | An optional label, unique among keys not revoked. |
| `planted` | string | yes | Where it was planted, in the operator's words: what an alert says so that the leak can be found. |
| `state` | string | yes | armed: its next use raises an alert. fired: it was used since it was armed, so more uses count and only now and then alert again, until it is reset. revoked: it is over and answers key_revoked. |
| `tags` | list of string | yes |  |

### `CanaryList`

| Field | Type | Required | Description |
|---|---|---|---|
| `canaries` | list of [Canary](#canary) | yes |  |

### `CanaryOrigin`

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | no | The client address. Left out unless the expose_client_addresses setting is on. |
| `address_withheld` | boolean | no | There is an address, which is left out because expose_client_addresses is off. |
| `as_org` | string | no | The organisation the ASN belongs to, when the ASN database names one. |
| `asn` | integer | no |  |
| `client` | string | no | What called: the User-Agent reduced to a product and major version, such as curl/8, or none. |
| `country` | string | no | An ISO 3166-1 alpha-2 code, when a GeoIP database names one. |

### `CanaryUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `ignore` | list of string | no | The new ignore list, which replaces the old one; empty clears it. |
| `planted` | string | no | A new place it was planted. |
| `tags` | list of string | no | The new tags, which replace the old ones. |

### `Channel`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `delivered` | integer | no | How many alerts it has been sent, until they are pruned. |
| `failed` | integer | no | How many alerts were given up on. |
| `host` | string | yes | The host of its address, which is all of the address that is shown. |
| `id` | string | yes |  |
| `kind` | string | yes | slack or webhook. |
| `last_error` | string | no | Why the latest failed attempt failed. Never the address. |
| `lifecycle_alerts` | boolean | yes | Whether it also takes spend and lifecycle warnings. |
| `min_severity` | string | yes | The lowest severity it takes: low, medium, high or critical. |
| `name` | string | yes |  |
| `pending` | integer | no | How many alerts wait in the outbox for it. |

### `ChannelList`

| Field | Type | Required | Description |
|---|---|---|---|
| `channels` | list of [Channel](#channel) | yes |  |

### `ChannelTestResult`

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | string | yes | The channel's name. |
| `error` | string | no | Why the test alert did not arrive. Never the address. |
| `ok` | boolean | yes |  |

### `CompromiseCredentialRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `incident_id` | integer | no | The incident this is done for. It must exist, be unresolved and be about a key on this credential. The compromise and that key's rotation are audited with its id, and it is resolved as confirmed. |
| `secret` | string | yes | The new real secret, made at the provider after the leaked one was revoked there. kman seals it and never shows it again. |

### `CompromisedCredential`

| Field | Type | Required | Description |
|---|---|---|---|
| `credential` | [Credential](#credential) | yes |  |
| `rotated_keys` | list of [RotatedKey](#rotatedkey) | yes | Every virtual key on the credential that was not revoked, oldest first, with its new secret, shown only this once. Each is an app that needs its new key. |

### `ContainKeyRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `incident_id` | integer | no | The incident this is done for. It must exist, be about this key and be unresolved. The audit entry carries its id, so the incident's timeline shows the action. Disabling leaves the incident open; revoking resolves it as confirmed. |

### `CreateAdminTokenRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit. A revoked token's name stays taken. |

### `CreateCanaryRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `base_url` | string | no | kman's proxy URL, as an app would be told it. When given, the snippet sets the variable's base URL too, so a thief who finds the snippet alone can reach kman with it. |
| `format` | string | no | The snippet to plant: env (a .env line), mcp (an MCP server config's env block), shell (an export line) or ci (a gh secret set command). Defaults to env. |
| `ignore` | list of string | no | Addresses and networks (CIDR) whose use does not fire the canary. At most 100, none wider than an IPv4 /16 or an IPv6 /32. |
| `name` | string | no | An optional label: 1 to 63 of a-z, 0-9, '.', '_' and '-', starting with a letter or digit, unique among keys not revoked. It stays out of the snippet. |
| `planted` | string | yes | Where it will be planted: 1 to 200 characters, with no control characters. An alert names it. |
| `tags` | list of string | no | At most 32 labels of a-z, 0-9, '.', '_', ':' and '-'. |
| `var` | string | no | The variable the snippet sets, such as OPENAI_API_KEY (the default). A thief finds a key by the name it is under. |

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

### `CreatedCanary`

| Field | Type | Required | Description |
|---|---|---|---|
| `canary` | [Canary](#canary) | yes |  |
| `format` | string | yes | The format of the snippet. |
| `secret` | string | yes | The canary's key (kmn_...), in the standard format: nothing in it says it is a canary. kman keeps only its hash, so it is shown only this once. |
| `snippet` | string | yes | What to plant, in the format asked for. It holds the secret, so it is shown only this once, and it says nothing of what it is: not the canary's name, where it is planted, or its tags. |

### `CreatedChannel`

| Field | Type | Required | Description |
|---|---|---|---|
| `channel` | [Channel](#channel) | yes |  |
| `signing_secret` | string | no | For a webhook, the secret that signs each request (kmwhsec_...), shown only this once. A receiver recomputes the signature with it. |

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

### `Event`

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer | no | The autonomous system number, from the ASN database, when there is one that knows the address. |
| `asn_label` | string | no | The autonomous system's organisation, when the ASN database names one. |
| `client` | string | no | What made the request: its User-Agent reduced to a product and major version, such as openai-python/3 or curl/8, or none when it sent no User-Agent. Never the header itself. Empty for an event stored before kman recorded it. It is not personal data, so it is always shown. |
| `client_address` | string | no | The client address: the connection's, or X-Forwarded-For only through trusted_proxies. Left out unless the expose_client_addresses setting is on. |
| `client_address_withheld` | boolean | no | The event has a client address, which is left out because expose_client_addresses is off. |
| `country` | string | no | ISO 3166-1 code, from the country database, when there is one that knows the address. |
| `id` | integer | no | The stored event's id, which pages by. Not set on an event from the live stream, which is not stored yet. |
| `key_id` | string | no | The virtual key's id. Empty when the request presented no key that kman could identify. |
| `key_name` | string | no |  |
| `kind` | string | yes | forwarded: kman sent the request to an upstream, or it was a realtime session. refused: kman answered it itself, with a refusal code. |
| `model` | string | no |  |
| `operation` | string | no | The request's method and path, such as POST /v1/chat/completions. Never a query string. |
| `refusal_code` | string | no | The error code of a refusal, such as key_disabled or invalid_key. |
| `status` | integer | no | The status the app was sent. |
| `time` | string | yes |  |
| `unidentified_key` | boolean | no | The request presented no key that kman could identify: a key that does not exist, or none. Many of these from one place is probing. |

### `EventPage`

| Field | Type | Required | Description |
|---|---|---|---|
| `client_addresses_withheld` | boolean | no | Client addresses were left out because expose_client_addresses is off. |
| `events` | list of [Event](#event) | yes |  |
| `more` | boolean | yes | Whether there are older events that match. |
| `next_before` | integer | no | The id to send as before for the next, older page. Set when more is true. |

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

### `Incident`

| Field | Type | Required | Description |
|---|---|---|---|
| `acknowledged_at` | string | no |  |
| `acknowledged_by` | string | no |  |
| `detection_count` | integer | yes | Every detection raised, repeats included. The detail lists at most 100 of them, its escalations first among them. |
| `id` | integer | yes | A plain integer, as alerts and commands name it. |
| `key_id` | string | yes |  |
| `key_name` | string | no |  |
| `last_fired_at` | string | yes |  |
| `opened_at` | string | yes |  |
| `quiet` | boolean | no | Nothing has fired for an hour. Display only: the incident is still unresolved, and a new firing on a quiet incident alerts again. |
| `resolution` | string | no | confirmed, false_positive or expired. |
| `resolved_at` | string | no |  |
| `resolved_by` | string | no | An admin token's name, or system:detect when it expired. |
| `rules` | list of string | yes | The rules that have fired, in the order they first did. |
| `severity` | string | yes | low, medium, high or critical: the highest of its detections. |
| `state` | string | yes | open, acknowledged or resolved. |

### `IncidentDetail`

| Field | Type | Required | Description |
|---|---|---|---|
| `acknowledged_at` | string | no |  |
| `acknowledged_by` | string | no |  |
| `actions` | list of [AuditEntry](#auditentry) | yes | The audit entries that carry the incident's id: its opening and escalations by system:detect, and what operators did about it. With the detections, the timeline. |
| `addresses_withheld` | boolean | no | A client address or network was left out of the evidence because expose_client_addresses is off. |
| `detection_count` | integer | yes | Every detection raised, repeats included. The detail lists at most 100 of them, its escalations first among them. |
| `detections` | list of [IncidentDetection](#incidentdetection) | yes | What fired, oldest first, at most 100: the detections that escalated the incident (the first and the newest, if there are more), then the oldest and the newest of the rest. detection_count says how many there were. |
| `id` | integer | yes | A plain integer, as alerts and commands name it. |
| `key_id` | string | yes |  |
| `key_name` | string | no |  |
| `known_source_candidates` | list of [KnownSourceCandidate](#knownsourcecandidate) | yes | The places the detections saw, which this was me can add as known sources. None for a canary. |
| `last_fired_at` | string | yes |  |
| `opened_at` | string | yes |  |
| `quiet` | boolean | no | Nothing has fired for an hour. Display only: the incident is still unresolved, and a new firing on a quiet incident alerts again. |
| `resolution` | string | no | confirmed, false_positive or expired. |
| `resolved_at` | string | no |  |
| `resolved_by` | string | no | An admin token's name, or system:detect when it expired. |
| `rules` | list of string | yes | The rules that have fired, in the order they first did. |
| `severity` | string | yes | low, medium, high or critical: the highest of its detections. |
| `state` | string | yes | open, acknowledged or resolved. |

### `IncidentDetection`

| Field | Type | Required | Description |
|---|---|---|---|
| `evidence` | [IncidentEvidence](#incidentevidence) | yes |  |
| `id` | integer | yes |  |
| `rule` | string | yes |  |
| `severity` | string | yes |  |
| `time` | string | yes |  |

### `IncidentEvidence`

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | no | The client address. Left out unless expose_client_addresses is on. |
| `address_withheld` | boolean | no | There is an address or network, which is left out because expose_client_addresses is off. |
| `as_org` | string | no |  |
| `asn` | integer | no |  |
| `baseline` | string | no | What the key normally does. |
| `client` | string | no | What called: the User-Agent reduced to a product and major version. |
| `country` | string | no |  |
| `detail` | string | no |  |
| `network` | string | no | The network (IPv4 /24, IPv6 /64). Left out unless expose_client_addresses is on. |
| `observed` | string | no | What the rule saw, for a rule that compares: a rate, a spend, a place. |
| `usual` | string | no | For a new network, the networks the key normally uses, busiest first. Left out unless expose_client_addresses is on. |

### `IncidentList`

| Field | Type | Required | Description |
|---|---|---|---|
| `incidents` | list of [Incident](#incident) | yes |  |
| `more` | boolean | yes | There are older incidents beyond this page: pass the last id as before. |

### `Key`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `credential` | string | no | The credential's name. Absent once a revoked key's credential is deleted. |
| `credential_id` | string | no |  |
| `expired` | boolean | yes | Whether its expiry has passed, whatever its status. |
| `expires_at` | string | yes | When the key stops working. Every key has one: there is no never. A canary has none, and shows the zero time. |
| `id` | string | yes | The key id: the non-secret handle inside the key, and how this API addresses it. |
| `kind` | string | yes | standard, or canary: a key with no credential that forwards nothing and raises an alert when used. A canary is not in the list of keys; GET /canaries lists them. |
| `name` | string | no | An optional label, unique among keys not revoked. |
| `note` | string | no |  |
| `policy` |  | yes | The key's policy, a JSON object: empty until it is edited. |
| `policy_version` | integer | yes | The version of the policy format the policy is in. |
| `spend` | [KeySpend](#keyspend) | no | What the key has spent today and this month, beside its caps. Shown when the key is read, and not in the audit log. |
| `status` | string | yes | active, disabled or revoked. |
| `tags` | list of string | yes |  |
| `upstream` | string | no | The upstream's name. Absent once a revoked key's upstream is deleted. |
| `upstream_id` | string | no |  |

### `KeyClient`

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | yes | The client address: the connection's, or X-Forwarded-For only through trusted_proxies. |
| `asn` | integer | no | The autonomous system number, from the ASN database, when there is one that knows the address. |
| `blocked` | integer | yes | Requests kman refused: a locked address, a refused key, a policy. |
| `cost_micro` | integer | yes | The known cost of its requests, in microdollars. |
| `country` | string | no | ISO 3166-1 code, from the country database, when there is one that knows the address. |
| `first_seen` | string | yes |  |
| `last_seen` | string | yes |  |
| `requests` | integer | yes | Forwarded requests and realtime sessions. |
| `unmetered_requests` | integer | no | Forwarded requests whose cost is not known, which cost_micro does not include. |

### `KeyClients`

| Field | Type | Required | Description |
|---|---|---|---|
| `clients` | list of [KeyClient](#keyclient) | yes | The addresses the key was used from, the most recently seen first, at most 50. A request that came in on a Unix socket has no address and is not counted. |
| `key_id` | string | yes |  |
| `total` | integer | yes | How many distinct addresses the key was used from. More than the length of clients when the list was cut. |

### `KeyList`

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | list of [Key](#key) | yes |  |

### `KeyPlaces`

| Field | Type | Required | Description |
|---|---|---|---|
| `key_id` | string | yes |  |
| `networks_withheld` | boolean | no | Network places were left out because expose_client_addresses is off. |
| `places` | list of [Place](#place) | yes | Most recently seen first. |

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

### `KnownSource`

| Field | Type | Required | Description |
|---|---|---|---|
| `created_at` | string | yes |  |
| `created_by` | string | yes |  |
| `id` | integer | yes | A plain integer. |
| `key_name` | string | no |  |
| `kind` | string | yes | cidr, country, asn or client. |
| `label` | string | yes | What the operator calls it. |
| `scope` | string | no | The key id it applies to. Empty: every key. |
| `value` | string | yes | A network in CIDR form, an ISO 3166-1 country code, an AS number without the AS, or a client (a User-Agent reduced to a product and major version, such as curl/8). |

### `KnownSourceCandidate`

| Field | Type | Required | Description |
|---|---|---|---|
| `kind` | string | yes | cidr, country, asn or client. |
| `label` | string | no |  |
| `value` | string | no | The value to add. Empty for a network while expose_client_addresses is off. |
| `withheld` | boolean | no |  |

### `KnownSourceList`

| Field | Type | Required | Description |
|---|---|---|---|
| `known_sources` | list of [KnownSource](#knownsource) | yes |  |

### `NewCredential`

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | Defaults to the upstream's name. |
| `secret` | string | yes | The real secret. kman seals it and never shows it again. For basic auth without a username, the whole user:password. |

### `Place`

| Field | Type | Required | Description |
|---|---|---|---|
| `first_seen` | string | yes |  |
| `kind` | string | yes | country, asn, network or client. |
| `label` | string | no | A name for the value, when kman has one: an AS's organisation, from the ASN database. |
| `last_seen` | string | yes |  |
| `other` | boolean | no | This row counts every place of its kind past the first 1,000 the key was seen from. |
| `refused` | integer | yes | Requests kman refused from here. |
| `requests` | integer | yes | Requests kman forwarded from here, and realtime sessions. |
| `value` | string | yes | An ISO 3166-1 alpha-2 country code, an AS number, an IPv4 /24 or IPv6 /64 network, or a client: the User-Agent reduced to a product and major version, such as openai-python/3, or none. Empty for the other row. |

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

### `PruneRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `audit_log` | boolean | no | Also delete the audit log's entries older than before. The one thing that ever deletes from the audit log. The prune is recorded in the log, and that record stays. |
| `before` | string | yes | Delete what is older than this: events, hourly usage, and alerts delivered before it. Not in the future. |

### `PruneResult`

| Field | Type | Required | Description |
|---|---|---|---|
| `audit_entries` | integer | yes | Audit log entries deleted. 0 unless audit_log was set. |
| `before` | string | yes |  |
| `delivered_alerts` | integer | yes | Alerts deleted that were delivered before the date. Pending and failed alerts are kept. |
| `events` | integer | yes | Events deleted. |
| `hourly_usage` | integer | yes | Hourly usage rows deleted: those whose hour ended before the date. |

### `ResolveIncidentRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `as` | string | no | confirmed (the default) or false_positive (false-positive is accepted). A false positive lets what the incident held out of the key's baseline back in, so it is the operator's call. expired is only ever set by kman. |

### `RotateKeyRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `grace_seconds` | integer | no | How long the old secret keeps working beside the new one, at most 30 days. Absent or 0 ends it at once, on the next request. |
| `incident_id` | integer | no | The incident this is done for. It must exist, be about this key and be unresolved. The rotation is audited with its id and resolves it as confirmed. |

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
| `expose_client_addresses` | boolean | yes | Whether the admin API may tell which client addresses used a key (GET /keys/{id}/clients), the networks a key was used from (GET /keys/{id}/places), and the client address of each event (GET /events and its stream). Off by default: an address is personal data, and it stays out of the API until an operator turns it on. Turning it on and off is audited. |
| `learning_period_seconds` | integer | yes | How long, from its first forwarded request, a key's location rules (a new country, ASN, network or client) only record where it is used from, before they raise detections. A key's policy may set its own (learning_period). 0 is no learning period. Default 7 days, at most 90. A change applies at once, to keys that have already learned too: lowering it ends learning for the keys that have passed the shorter period, and raising it puts keys that had finished learning back into learning until the longer period, counted from each key's first forwarded request, has passed. |
| `max_key_lifetime_seconds` | integer | yes | The longest any virtual key may stay valid. No key is created or extended past it. Default 365 days. |
| `soft_budget_defaults` | list of integer | yes | The percentages of each spend cap or soft budget that raise a warning when crossed, rising, for keys whose policy sets none (soft_budget_percents). Each warns once per day or month. Default 50, 80, 100; an empty list warns for no key that sets none. A change takes effect at the next check, within a minute. |
| `spend_timezone` | string | yes | The IANA time zone in which daily and monthly spend periods start at 00:00. Default UTC. A change takes effect when kman restarts. |

### `SettingsUpdate`

| Field | Type | Required | Description |
|---|---|---|---|
| `default_key_ttl_seconds` | integer | no |  |
| `expose_client_addresses` | boolean | no |  |
| `learning_period_seconds` | integer | no |  |
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

### `ThisWasMeRequest`

| Field | Type | Required | Description |
|---|---|---|---|
| `add_known_sources` | boolean | no | Also add the places the incident saw as known sources, so they stop counting as new. Never for a canary. |
| `install_wide` | boolean | no | Add them for every key, not just this one. Needs add_known_sources. |
| `kinds` | list of string | no | Add only these kinds: country, asn, client, network. Default all. Needs add_known_sources. |
| `label` | string | no | The label for the known sources. Default says which incident they came from. Needs add_known_sources. |

### `ThisWasMeResult`

| Field | Type | Required | Description |
|---|---|---|---|
| `added` | list of [KnownSource](#knownsource) | yes | The known sources added. |
| `already_known` | integer | no | Places that were already known sources in that scope, so none was added. |
| `incident` | [Incident](#incident) | yes |  |
| `withheld_networks` | integer | no | Networks that were not added, because adding one would give it out while expose_client_addresses is off. |

### `UpdateStatus`

| Field | Type | Required | Description |
|---|---|---|---|
| `available` | boolean | yes | Whether Latest is newer than Current. |
| `checked_at` | string | no | When the last check ran, successful or not. |
| `current` | string | no | This binary's version, such as v0.1.0, or empty for a build that is not a release. |
| `enabled` | boolean | yes | Whether update_check is on in kman.yaml. kman asks the GitHub releases API once a day only when it is. |
| `error` | string | no | Why the last check failed, if it did. Latest is still what an earlier check found. |
| `latest` | string | no | The newest release the last successful check found. |
| `url` | string | no | The page of the latest release. |

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
