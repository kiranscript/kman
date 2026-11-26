# Facilitator guide

Use a fresh install per participant. Give the participant the [worksheet](worksheet.md). Keep setup, answers, and regression checks here.

## Prepare

Use Bash on Linux or macOS, the repository's Go toolchain, Node 22 or later, npm, and curl. Run these commands from the repository root.

```sh
(cd tui && npm ci && npm run typecheck && npm run build && npm test)
bash tui/test/usability/setup.sh
```

The script prints a `source` command. Open dedicated Bash terminals in the repository root and run that command in each. It selects the freshly built CLI, a private config and data directory, and a unique admin socket. The fixture supplies every credential. Allow at least 80×24 cells for the TUI.

- In the first terminal run `kman serve`. Note the loopback port on its **proxy listening** line. It changes on restart.
- In the second run `node tui/test/usability/provider.mjs`. Note the **mock upstream** port. This local service replays the repository's chat-completion fixture.
- In the participant terminal authenticate once and launch the built client.

```sh
kman login --admin-url "$KMAN_ADMIN_URL" < "$KMAN_LAB/owner-token"
node tui/dist/cli.js
```

## Run the tasks

**1. First key.** Start on the empty Status screen. Let the participant find the setup instruction. For this install, supply the local provider address and dummy credential. Use the following arguments with the mock upstream's printed port in a lab shell. The participant can quit and reopen the TUI, or leave it running while using another lab shell.

```sh
read -rp 'Mock upstream port: ' mock_port
DUMMY=usability-only kman upstream add openai --preset openai \
  --host 127.0.0.1 --port "$mock_port" --no-tls --credential-env DUMMY
```

After key creation, let the participant paste **only the saved virtual key** into this hidden prompt. Use the proxy port from `kman serve`. A successful response contains `Hello there`. This also works when the terminal blocks clipboard writes and the participant manually selects the secret.

```sh
read -rp 'kman proxy port: ' proxy_port
read -rsp 'Saved virtual key: ' virtual_key
echo
printf 'Authorization: Bearer %s\n' "$virtual_key" |
  curl -fsS -H @- -H 'Content-Type: application/json' \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}' \
    "http://127.0.0.1:$proxy_port/v1/chat/completions"
unset virtual_key
```

**2. Spend warning.** Start the in-memory demo with `(cd tui && ./node_modules/.bin/tsx test/demo.tsx)`. The expected key is `ci-evals`, at $0.90 of $1 daily with $0.10 remaining. The other near-cap key, `prod-api`, is at 86%. Quit and relaunch the demo to reset its data.

**3. Key lifecycle.** On the real install with the test upstream added, seed the pair once. Discard their generated secrets. Launch or return to `node tui/dist/cli.js`.

```sh
kman key create service-checkout-east --upstream openai >/dev/null
kman key create service-checkout-west --upstream openai >/dev/null
```

After canceling revoke, check that west is active. At the end, Audit should show one disable, one enable, and one revoke for west. East should remain active. Cross-check both statuses with `kman key list`. To inspect a revoked key individually with the CLI, use its ID.

**4. Recovery.** While the TUI shows populated data, stop the foreground server with Ctrl+C. Allow one poll. The last good data should stay visible with an offline indicator, and a key change should be blocked. Also open a fresh TUI while the server is down and check its startup guidance. Restart `kman serve` in the same lab shell. Both clients should recover. For the refused-token task start `KMAN_ADMIN_TOKEN=kma_disposable_invalid node tui/dist/cli.js`. This override lasts one launch. Quitting and starting normally uses the valid saved credentials. For a fresh login, use the lab's admin address and owner-token file as above.

## Repeat and reset

Run the affected steps at 80×24, 99×30, 100×30, and 140×40. Try `NO_COLOR=1 node tui/dist/cli.js`. Resize with a key filter active and again after typing part of the revoke name. Check the target and typed text survive, the footer reflects focus, and resizing alone never submits a change.

For the live API suite, run this **after** the participant tasks. It creates and revokes another disposable key.

```sh
(cd tui && KMAN_TUI_LIVE_URL="$KMAN_ADMIN_URL" \
  KMAN_TUI_LIVE_TOKEN="$(cat "$KMAN_LAB/owner-token")" npm test)
```

To reset the real install, quit its clients, stop its server and mock provider with Ctrl+C, and remove the specific temporary directory printed by setup. Close the dedicated shells. Run setup again and source the new environment for the next participant. For a lifecycle-only session, add the test upstream and seed the pair on the fresh install before starting.

## Phase 1 run

On Linux with Node 24, the initial build and typecheck passed. The baseline suite passed 267 tests with the four opt-in live cases skipped. All four then passed against the real server. After the fixes the combined run passed all 272 tests, including the new resize regression and the four live cases.

The terminal walkthrough verified empty-install guidance, a copied key returning HTTP 200 through the local proxy, and the spend warning in the seeded demo. The west-key actions appeared in Audit and east stayed active. Stopping and restarting the server exercised both cached and cold-start recovery. A refused token showed the endpoint and override guidance. Filter and confirmation checks covered all four terminal sizes, including a monochrome run.

Dialogs now retain their input when resizing moves them beside the list. The login hint now identifies the admin address and explains how to recover from a token override. Human observations and terminal-emulator clipboard permissions remain for Phase 2.
