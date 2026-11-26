# Phase 2 usability session: Opus agent operator

**One Claude Opus 5.5 agent ran the worksheet at high effort as a first-time kman operator.** Human validation and clipboard testing remain open. The agent used the screen buffer to retrieve the disposable key.

## Participant, method, terminal

- **Participant:** Claude Opus 5.5, acting as an operator who had never used kman. It stayed blind to sources, tests, the facilitator guide, earlier threads and git history. It used only what was on screen, the TUI's `?` help, `kman --help`, `kman upstream add --help` and `kman-tui --help`.
- **Terminal:** tmux 3.4 on its own socket, 80x24, `TERM=xterm-256color`, colour on. It ran on WSL2 Linux. The agent sent keys with `send-keys` and read the screen with `capture-pane`.
- **Builds:** the live client was `node tui/dist/cli.js` against the disposable lab install over a unix admin socket. The demo was `tsx test/demo.tsx`.
- **Facilitator help (staging, not UI coaching):** the mock provider's parameters (OpenAI-compatible, 127.0.0.1:45781, no TLS, test credential), and the fixture controls `verify`, `seed`, `stop` and `start`. No UI action was coached.
- **Secrets:** a script read the one-time key from the pane and piped it straight into `fixture verify`. It was never printed or saved. Every capture shown below that passed near a secret was redacted.

## Outcomes

| Task | Outcome | First hesitation or wrong turn | Expected vs observed |
|---|---|---|---|
| 1 Get started | **Unaided** | The empty-state hint gives only `kman upstream add openai --preset openai`. I had to work out from `upstream add --help` that `--host/--port/--no-tls` go with a preset. That worked on the first try. | Expected: add the upstream with the CLI, then create the key in the TUI. Observed as expected. `n` opened a form with the only upstream filled in; `Ctrl+S` created the key and showed the secret screen; `verify` returned a chat completion (exit 0). |
| 2 Investigate spending | **Unaided** | None. The "remaining" amount is not shown anywhere, so I worked it out. | Status shows two `▲ near cap` keys. In the detail pane, ci-evals reads "▲ 90% of its daily cap", `$0.900 / $1`, and prod-api reads "86%", `$4.30 / $5`. **Answer: ci-evals, $0.90 spent today, $1 cap, $0.10 left (my arithmetic).** |
| 3 Change the right key | **Unaided** (after a staging fix I did myself) | My name for the task 1 upstream, `mock`, broke `fixture seed` ("upstream openai does not exist"). I added a second upstream called `openai` with the same parameters and seeded again. This was a mismatch with the staging, not with the UI. | `d` then `y` disabled the key, and `e` then `y` enabled it again. With `x`, I typed a partial name and pressed Esc: the key stayed `● active` and Audit stayed at 10 entries. `x`, the full name ("Name matches."), then Enter revoked it. The detail read "✖ revoked … ended for good". service-checkout-east stayed `● active`, and Audit showed disable, enable and revoke. |
| 4 Recover access | **Unaided** | The outage screen said `Start it with: kman serve`. I used `fixture start` instead, because the facilitator controls the service. The `kman serve` advice was not tested. | The open client showed `◌ offline · data Ns ago` and kept the data it already had. Actions were refused with "kman is not answering, so nothing can be changed now. r tries again." A cold start showed a "Cannot reach kman" dialog that retries every few seconds. Both clients reconnected on their own after `start`. A refused token showed a clear dialog, and `unset KMAN_ADMIN_TOKEN` plus a restart brought back a live session. |

## Evidence

- **Task 1, after the upstream:** Status `▲ mock plain http`. Keys `(0) No keys yet. n creates one.` The upstream detail listed routes and `sdk env OPENAI_API_KEY`.
- **Task 1, secret screen:** it said "Copy it now. kman keeps only its hash…", "Set OPENAI_API_KEY to the secret above.", "Base URL: kman's proxy address + /v1 (http://127.0.0.1:8080 by default)" and "The secret is cleared from this screen when you press Enter." After Enter and a refresh: `workshop-first ● active ○ no cap <$0.001`, `1 request`.
- **Task 1:** the key form warned "▲ With no cap, a leaked key can spend without limit." I left the optional caps empty.
- **Task 2:** Keys list row: `ci-evals ● active ▲ near cap $0.900` and `prod-api ● active ▲ near cap $4.30`. The list rows show no percentages; only the detail pane does.
- **Task 3:** each change showed a toast and a new Audit entry: "✔ Disabled service-checkout-west. It is refused from its next request.", "✔ Enabled service-checkout-west.", "✔ Revoked service-checkout-west. It cannot be used again." The revoke dialog says "To stop it for now, disable it instead." Cancelling with Esc showed no toast.
- **Task 4, open client:** the top line went from `data 5s ago` to `data 23s ago`. The footer changed `n new key` to `r retry` and showed `◌ offline`. Pressing `r` showed "✖ Could not refresh: cannot reach unix:…/admin.sock: connection refused".
- **Task 4, refused token:** the dialog was titled "The admin API refused this token" and said "the admin token is unknown or revoked", gave the admin address, `kman login --admin-url <address>`, and "If KMAN_ADMIN_TOKEN is set, replace or unset it. Then restart the TUI." The status bar read `✖ token refused`.

## Friction, in priority order, with retest criteria for Phase 3

1. **P1: a stale error stays on screen after reconnecting.** After `fixture start`, the open client's indicator went back to `● live` while "✖ Could not refresh: … connection refused" stayed on screen. It was still there 27 seconds later and only cleared on a manual `r`. The screen contradicts itself about whether data is fresh.
   *Retest:* within one automatic refresh of the service coming back, the open client shows `● live` and no outage error remains in the message line.
2. **P1: rapid list navigation advanced fewer rows than expected in tmux.** In the demo Keys list, `jjj` sent in one write did not move the selection at all (it stayed at "1 of 7"). `Down Down Down` moved it once ("2 of 7"), and `j j` moved it once. Keys sent about 0.4 s apart all registered. This could come from how tmux delivers keys, but key-repeat and fast typing could land the operator on the wrong key. The detail title and the confirm dialogs, which name the key, reduce the risk.
   *Retest:* in a human terminal and in tmux, three fast `j` or `↓` presses move the selection exactly three rows.
3. **P2: the spending question needs arithmetic and opening each key.** No list row or Status line shows a percentage or the amount left. Both near-cap keys say only `near cap`, and the one spending more dollars (prod-api, $4.30) is not the one closest to its cap (ci-evals, 90%).
   *Retest:* an operator can name the key nearest its daily cap, with its percentage, spend, cap and remaining amount, without opening more than one detail pane and without doing arithmetic.
4. **P2: the first setup step leaves out what a non-default provider needs.** The empty-state hint `kman upstream add openai --preset openai` says nothing about `--host/--port/--no-tls`. The secret screen gives only the *default* proxy URL (`127.0.0.1:8080`), so nothing on screen confirms the address this install actually serves.
   *Retest:* a first-time operator adds a custom-host upstream on the first try from the on-screen hint or `--help`, and can read the install's real proxy base URL from the TUI.
5. **P2: the refused-token dialog hedges about state it can see.** It says "If KMAN_ADMIN_TOKEN is set…" even though the variable was set and was overriding a saved login that worked.
   *Retest:* when the refused token came from `KMAN_ADMIN_TOKEN`, the dialog says so and names unsetting it as the first recovery step.
6. **P3: revoke is hard to discover from the footer at 80 columns.** With more than one key the footer drops `x revoke` (it shows `↑↓ select … d disable / filter`); it appeared only when there was one key, and in `?` help.
   *Retest:* at 80x24 with several keys, an operator finds revoke without opening help, or help is plainly signposted for actions that don't fit.
7. **P3: cancelling a revoke gives no confirmation.** After Esc there was no toast; I checked the row and the Audit count to be sure nothing had happened.
   *Retest:* after a cancelled revoke the message line confirms the key is unchanged.
8. **P3: lower-value observations.** The `▲ plain http` warnings for local mock upstreams fill the "Needs attention" list. The first revoke dialog drew at the top of the screen and the second was centred (seen once, low confidence).

## Not covered

- Human operator behaviour, think-aloud comments and timing.
- The clipboard `c copy` path.
- Other terminal sizes and NO_COLOR.
- `kman serve` and `kman login` as recovery paths.
- Behaviour of the key form with two upstreams.

After the session, the facilitator checked the real install. The audit history for west contained creation, disable, enable and exactly one revoke. East and workshop-first were both active and had only creation entries.

## Verification after fixes

- Stopping the disposable server and requesting a refresh produced an offline error. Restarting it restored the live indicator and cleared the error without keyboard input.
- A burst of three navigation keys moved three rows in tmux and in a direct PTY. The filtered-list regression also checks that repeated letters still enter text in the filter.
- At 80×24, 99×30, 100×30 and 140×40, Status showed the daily cap percentage for ci-evals and its detail showed the remaining $0.10.
- The empty install displayed the custom endpoint flags. The secret screen showed the actual bound loopback proxy port with the SDK path. A refused token supplied through the environment was identified by source.
- At 80×24 the Keys footer showed revoke. Cancelling identified the key in the notice. The first and second revoke dialogs both opened on row 8; the earlier placement difference could not be reproduced.
- The local mock upstream's HTTP transport stayed visible in its detail. It no longer added an entry to Needs attention solely for using loopback HTTP.
