# Prompt for AI coding assistants: build a hamstack plugin

Copy everything between the lines into your assistant, fill the `<...>` parts, and put your idea at the end. Ask it to
run the validator and show the output. (hamstack plugins are a **draft**: the validator is the source of truth.)

---

You are building a **hamstack plugin**: a separate program with its own small web server that hamstack (a ham-radio
station manager) installs from a GitHub release, runs as a user service, and shows inside its dashboard at
`/plugin/<id>/` behind the dashboard's login. Follow these requirements exactly; do not invent manifest fields, placeholders or
environment variables that are not listed here. Your own program's flags are yours to name (for example `-listen`,
`-base-path`, `-rigctld`) as long as `run.command` passes them using the placeholders; never invent a flag that
switches plugin mode, because plugin mode is `HAMSTACK_PLUGIN_ID`.

**Deliverables**
1. The program, source and tests, in `<language>` (prefer one static binary with web assets embedded).
2. `hamstack-plugin.json`, validated with `hamstack plugin validate hamstack-plugin.json` until it prints `valid`.
   If you have it, docs/plugins/examples/rigctlweb.json is a valid sample; unknown keys are errors.
3. A GitHub Actions workflow that, on a `v*` tag, builds linux/amd64 and linux/arm64 and publishes assets named
   `<name>-{tag}-linux-{arch}` plus `sha256sums.txt` (sha256sum format).
4. A README whose first screen says what the plugin does, whether it can transmit, and what it was tested on.

**Runtime contract (all required)**
- Listen on the address given by a flag or `HAMSTACK_LISTEN`-style input you document in the manifest's `run.command`
  using `{listen}` (127.0.0.1:PORT). Other placeholders: `{base_path}` `{port}` `{data_dir}` `{config_dir}` `{rigctld}` (host:port). Default to 127.0.0.1; never 0.0.0.0.
- Accept a base path (`{base_path}`, like `/plugin/<id>`, no trailing slash). Serve pages, API, WebSocket and static
  files under it. Every URL in HTML/JS must be relative or start with the base path (e.g. `/plugin/<id>/static/app.js`), and every cookie path must be the base path. No absolute `/api`, no `Path=/`.
- Plugin mode is on when `HAMSTACK_PLUGIN_ID` is set (never a flag of your own invention): then no login, no cookies, one rig taken from `{rigctld}`, no way to add connection targets, `frame-ancestors 'self'`. hamstack's login protects you. Bind loopback; require a custom header or CSRF token on
  state-changing requests and check `Origin`.
- Health endpoint returning 200 when the program can serve its page. It must not depend on the radio being reachable (hamstack starts rigctld first; rig trouble is reported in your API and page, not by failing health); set `run.health` to it.
- Exit within 5 seconds of SIGTERM.
- Radio access only as a **client of rigctld** at `{rigctld}` / `HAMSTACK_RIGCTLD` (`host:port`). Never open a serial port.
- If anything can key the transmitter: it must happen only on an explicit person action, must unkey on SIGTERM, on
  crash/panic, on client disconnect and after a watchdog timeout, and the manifest must set `needs.transmits: true`.
- Data only in `HAMSTACK_DATA_DIR`, settings only in `HAMSTACK_CONFIG_DIR`. Never delete files you did not create.
- No secrets in `run.env` or command lines; use `config` entries with `"secret": true`.
- `-version` prints one line with a semantic version. Logs go to stdout/stderr without colour codes.

**Tests you must write and run**
- A test that starts the server with a base path and asserts every URL in the served HTML starts with it.
- A test that starts it against a fake rigctld, sends SIGTERM, and asserts the radio was unkeyed and exit took < 5 s.
- Run `hamstack plugin validate` and include its output in your final answer.

**Before you finish, report:** the validator output; every place you were unsure of the spec; and anything you
assumed. Do not claim the plugin works inside hamstack: the runtime is not built yet; say that you tested it standalone
behind a prefix-keeping reverse proxy.

**Mode** (delete the one that does not apply)
- **CREATE:** build a new plugin. **My idea:** <describe the plugin>
- **UPDATE:** make the existing project in the current folder compatible. Do it in this order and stop after step 2
  for my review unless I said to continue:
  1. *Audit, change nothing.* Search the code for everything the runtime contract above forbids or lacks: absolute
     URLs (`/api`, `/ws`, `/_app`, asset links) in server code AND front-end code, cookie paths, own login/token,
     framing headers (`X-Frame-Options`, CSP `frame-ancestors`), how rigs are chosen (any way to add arbitrary
     targets), shutdown timeout and order (unkey first), unkey on last client leaving / panic / start-up, config and
     data locations, version output, release asset names and checksums.
  2. *Report* each finding as file:line, the rule it breaks, and the smallest fix. Mark every flag or variable you
     would add as "to be added". Do not rename or remove existing flags: keep standalone use working.
  3. *Change*, smallest diff first, one rule at a time, with its test. Standalone behaviour must be unchanged when
     `HAMSTACK_PLUGIN_ID` is not set (add a test that proves it).
  4. Write `hamstack-plugin.json`, validate, and report as above.

---
