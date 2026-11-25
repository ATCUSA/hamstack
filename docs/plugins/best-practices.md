# Plugin best practices

## Radio safety (most important)
- **The radio is shared.** hamstack's rig service owns the serial port. Connect to rigctld as a client; never open the port.
- **Unkey on every exit path**: SIGTERM, SIGINT, panic/crash handlers, a client disconnecting, a WebSocket closing, a
  timeout. A watchdog that keys off after N seconds of no heartbeat is cheap and worth it.
- **No automatic transmit.** Anything that keys the radio happens on a person's explicit action, once. Tune-type
  actions are off until the owner enables them in hamstack.
- **Reads before writes.** Poll gently (about 1/s per meter set); a plugin must not saturate the CAT link the modem needs.
- Declare `needs.transmits` honestly. If you transmit audio, declare `needs.audio` too.

## Security
- Bind loopback. hamstack's proxy is the only way in; do not widen it.
- No second login behind the dashboard's. Do not store the dashboard's cookies or read its token.
- Treat every request as from a logged-in user *and* a possibly hostile web page: require a custom header or a CSRF
  token on state-changing requests, check `Origin`, never accept commands as shell strings.
- Secrets: manifest `config` entries with `secret: true`; never in `run.env`, command lines, logs or error text.
- Verify what you download. Publish `sha256sums.txt`; do not self-update behind hamstack's back.
- A plugin's page may be framed by the dashboard: allow `frame-ancestors 'self'`, not `'none'` and not `*`.

## Packaging
- One static binary per platform; embed the web assets. Name assets from one template.
- `-version` prints one line with a semantic version. Releases are tagged `vX.Y.Z`.
- Be a good citizen on disk: only `{data_dir}` and `{config_dir}`; never delete anything you did not create.
- Uninstall must be safe: hamstack removes the program and its service; it will never delete your data folder without asking.

## Behaviour
- Start in under 2 seconds; answer `run.health` only when you can really serve.
- Exit within 5 seconds of SIGTERM.
- Log to stdout/stderr (hamstack captures it in the journal); no log files, no ANSI colour codes.
- Fail loudly at start-up for bad configuration, with a message that names the setting.

## Testing
- Unit-test the exit path: start, key, send SIGTERM, assert unkeyed.
- Test with a fake rigctld (a TCP server that speaks `f`, `m`, `t`, `T`) so CI needs no radio.
- Test under the prefix: every URL in the served HTML starts with `{base_path}/`.
- Run `hamstack plugin validate` in CI.

## Community
- One plugin, one job. Prefer small and boring.
- Document what the plugin transmits and when, in the README's first screen.
- Say what you tested on (radio, rigctld/hamlib version, OS).

## Voice and audio plugins
- **One transmit path.** Only one browser session can hold PTT at a time; a second one is told who has it.
- **Push-to-talk, not open mic.** Transmit only while the person holds the control; release, blur, disconnect, page hide and a
  lost WebSocket all unkey. Add a transmit time limit (TOT) that cuts the carrier by itself.
- **Identification.** Remind the operator when the station last identified; never transmit an ID on their behalf unless they asked.
- **Latency over quality.** Opus at 16-24 kHz mono is plenty for voice; small frames (20 ms), a short jitter buffer, drop late audio.
- **Audio levels.** Never boost beyond what the person sets; show the TX level so overdriving the radio is visible.
- **Secure context.** The microphone needs HTTPS; say so clearly instead of failing silently when it is missing.
