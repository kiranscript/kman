# Operator worksheet

Use the terminal and its built-in help. Say what you expect before taking an action and what on screen tells you it worked. The facilitator will switch between a disposable install and a populated demo. Use the supplied test credential for setup. Keep tokens out of notes and recordings.

1. **Get started.** This install has no upstream or virtual keys. Set it up for the supplied test provider, create a key for `workshop-first`, and save the one-time secret. Use the saved value for the facilitator's local test request.
2. **Investigate a leak.** In the populated demo, which key has been used from somewhere new? Where did those requests come from, and what made them? Were they forwarded or refused, and why? Which command would show you the whole incident?
3. **Change the right key.** Back on the disposable install, pause and resume `service-checkout-west`. Begin revoking it and change your mind. Check that it is still active, then revoke it deliberately. Show what happened to that key and check the state of `service-checkout-east`.
4. **Recover access.** The facilitator will interrupt service, then provide a session whose token is refused. For each, describe what happened, what information you trust, and how you would get back to a live session. Try your recovery.

## Observer notes

Terminal and size __________  Prior experience with kman __________

| Task | Completed unaided, assisted, or blocked | First hesitation or wrong turn | Expected vs observed |
|---|---|---|---|
| Get started | | | |
| Investigate a leak | | | |
| Change the right key | | | |
| Recover access | | | |
