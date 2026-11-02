# kman

kman is a self-hosted proxy that holds real upstream API keys and hands apps virtual keys that can be limited, watched, and killed, so a leaked key is worth less and is easy to contain.

## Language

### Keys and tokens

**Virtual key**:
The secret an app sends to kman in place of a real upstream key. It is shown once; kman keeps only its hash.
_Avoid_: Token, API key (ambiguous with the real key)

**Key id**:
The non-secret handle inside a virtual key, used to look it up and to refer to it safely in logs, alerts, and UIs.
_Avoid_: Key name (a name is a separate, operator-chosen label)

**Canary key**:
A virtual key whose only purpose is to raise an alert when anyone uses it. Nothing in the key itself distinguishes it from a standard virtual key.
_Avoid_: Honeytoken, decoy key, canary token

**Child key**:
A short-lived virtual key minted from another virtual key, so a less-trusted client such as a browser can open a realtime session without holding the parent.
_Avoid_: Ephemeral token (that is the provider's own concept)

**Admin token**:
The secret an operator uses to manage kman. It is never accepted by the proxy as a virtual key.
_Avoid_: Admin key, master key (the master key is a different secret)
