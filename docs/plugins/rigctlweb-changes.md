# What rigctlWeb needs to become a hamstack plugin

Reviewed from `~/Projects/rigctlWeb` (Go backend + embedded SvelteKit static SPA, flags `-config -listen -rig -debug -version`).
These are requests to rigctlWeb; hamstack's spec is not bent to fit them.

| # | Gap | Why it matters | Change |
|---|---|---|---|
| 1 | Absolute URLs: `/api/...`, `/ws`, SPA served at `/` | Under `/plugin/rigctlweb/` every request misses the proxy prefix | Add a base-path option; serve all routes under it; build the SPA with relative URLs or inject the prefix |
| 2 | Session cookie `Path=/` (set and cleared) | Collides with the dashboard's cookies and ignores the prefix | Use the base path, or drop the cookie in plugin mode |
| 3 | Own token login | A second login behind the dashboard's | `-auth none` for plugin mode (still loopback only) |
| 4 | CSP `frame-ancestors 'none'` | The dashboard cannot show it in a frame | `frame-ancestors 'self'` (configurable) |
| 5 | `-rig host:port` single rig, `-rig` is run-only | hamstack has several rigs, one rigctld each | Accept `HAMSTACK_RIGCTLD`, or repeated `-rig id=host:port` |
| 6 | Unkeys on SIGTERM | Good: matches `stop_with: SIGTERM` | Keep; add a test; also unkey on last WebSocket close |
| 7 | Release assets already `rigctlweb-<tag>-<os>-<arch>` + `sha256sums.txt` | Good | Keep |

The example manifest assumes #1 as a `-base-path` flag; that flag does **not exist yet**, and #3 (`-auth none`) would be needed too. Do not use them
until rigctlWeb ships them.

## Found by a blind test of the AI prompt

An agent that was given only the prompt and spec produced a valid manifest. Beyond the table above it found, with
file:line references in its report: seven absolute `/api` fetches in `web/src/lib/api.ts`, the WebSocket URL in
`rig.svelte.ts`, absolute `href`s in `+layout.svelte` and `+page.svelte`, root-absolute `/_app/` asset URLs from the
SvelteKit build (the build-time base is the hard part: use relative assets or rewrite `index.html` at serve time and
recompute the CSP hash), `X-Frame-Options: DENY`, the 10 s shutdown timeout (contract: under 5 s) with `srv.Shutdown`
running before the unkey, no unkey when the last WebSocket client leaves (the doc comment promises it), no `recover`
that unkeys, `POST/DELETE /api/rigs` letting a user add arbitrary targets, and no unkey at start-up. Those are
requests to rigctlWeb, not hamstack changes.

## Second blind test (UPDATE mode) added

Same findings as above, plus: the manager's context is cancelled by SIGTERM, so the unkey runs on a client that was just
closed and may wait out a dial timeout (give the manager a context SIGTERM does not cancel, and unkey first);
`Unkey` ignores the `SetPTT` error and never retries; there is no start-up unkey; the start-up banner prints the token;
the active-link comparison in `+layout.svelte` must account for the base path; `packaging/systemd/rigctlweb.service`
listens on `0.0.0.0` (fine standalone, refuse non-loopback when `HAMSTACK_PLUGIN_ID` is set). The UI's login gate must follow
the plugin-mode "authenticated" answer, and the add/delete-rig controls must be hidden.
