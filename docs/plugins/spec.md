# Plugin manifest spec (draft, schema 1)

The manifest is one JSON file, `hamstack-plugin.json`, kept in the plugin's repository. Unknown keys are an error, so
a misspelling is caught instead of ignored. Run `hamstack plugin validate FILE` to check yours.

## Contract in one paragraph

hamstack downloads the release asset, checks its sha256, and runs `run.command` as a user service with the working
directory and variables below. It tells the program where to listen (always `127.0.0.1`, a port hamstack picks) and
what URL prefix it is served under. The program serves its page and API under that prefix, answers `run.health`
when ready, and exits cleanly on SIGTERM, releasing the radio first. hamstack's login protects the page; the plugin has
no login of its own.

## Fields

| Field | Required | Meaning |
|---|---|---|
| `schema` | yes | `1` |
| `id` | yes | 2-31 chars, `a-z 0-9 -`, starts with a letter. Becomes the URL `/plugin/<id>/` and the service name `hamstack-plugin-<id>`. Names hamstack uses (`api`, `vnc`, `apps`, `pat`, ...) are refused |
| `name`, `description` | yes | What people see. Description: one sentence, 200 characters at most |
| `version` | yes | Version when the manifest was written. hamstack asks the program itself (`install.version_cmd`) |
| `homepage`, `license`, `category` | no | `category`: winlink, modem, digital, aprs, tools, remote |
| `install.github` | yes | `owner/repo`. Releases come from GitHub tags |
| `install.tag_prefix` | yes | e.g. `"v"`; may be `""` |
| `install.asset` | yes | File name template. Placeholders: `{tag}` `{version}` `{os}` `{arch}` |
| `install.checksums` | yes | A file in the same release with sha256 sums (`sha256sum` format). **hamstack installs nothing it cannot verify** |
| `install.os`, `install.arch` | yes | What you publish. `linux`; `amd64`, `arm64` |
| `install.raw_binary` | no | The asset is the executable (not an archive) |
| `install.version_cmd` | no | Arguments that print the version, e.g. `["-version"]` |
| `run.command` | yes | argv. First word must be `{bin}`. Must contain `{listen}` or `{port}` |
| `run.env` | no | Fixed variables. Placeholders allowed. **Never secrets** (names containing password/token/secret are refused) |
| `run.health` | yes | Path that answers HTTP 200 when ready, e.g. `/api/health`. hamstack requests `{base_path}` + this path, i.e. the program serves it under its prefix like everything else. It means "the program can serve"; do not tie it to the radio being reachable |
| `run.stop_with` | yes | `SIGTERM`. See "Stopping" |
| `run.base_path` | yes | `arg`: a command word carries `{base_path}` (e.g. `-base-path {base_path}`). `env`: the program reads `HAMSTACK_BASE_PATH`, which hamstack always sets (nothing to map in the manifest). It is how the program learns its URL prefix. `none` is refused: see "Serving under a prefix" |
| `ui.title`, `ui.path` | yes | Dashboard label; start path (`/`) |
| `ui.embed` | yes | `frame` (inside the dashboard) or `tab` |
| `ui.permissions` | no | Browser permissions the page asks for, granted to its frame by the dashboard: only `microphone`. Requires `needs.audio`, and the dashboard over HTTPS (browsers refuse the microphone on plain HTTP except on localhost) |
| `ui.auth` | yes | `dashboard`: hamstack's login protects the plugin |
| `needs.rig` | yes | `none`, or `rigctld`: connects to hamstack's rigctld **as a client**. `serial` (opening the port itself) is refused |
| `needs.audio` | no | Opens the radio's sound card, **exclusively**: hamstack stops that rig's HF modem while the plugin runs, and will not start a modem under it (the same sharing rule the browser apps follow) |
| `needs.transmits` | no | Can key the transmitter. Raises the bar: see "Stopping" |
| `needs.network` | no | `loopback` (default) or `lan` |
| `config[]` | no | Settings hamstack shows and passes as env: `key`, `label`, `type` (text/number/toggle), `env`, `default`, `secret` |

## Placeholders

`{bin}` installed program · `{listen}` `127.0.0.1:PORT` · `{port}` · `{base_path}` e.g. `/plugin/rigctlweb` (no trailing
slash) · `{data_dir}` the plugin's own data folder · `{config_dir}` the plugin's own config folder · `{rigctld}` `host:port` of hamstack's rigctld for the first rig (only with `needs.rig: "rigctld"`). hamstack creates both folders (mode 0700) before starting. Both folders are
inside hamstack's per-plugin area; a plugin must not write elsewhere.

## Environment hamstack provides (draft)

`HAMSTACK_PLUGIN_ID`, `HAMSTACK_BASE_PATH` (same as `{base_path}`), `HAMSTACK_DATA_DIR`, `HAMSTACK_CONFIG_DIR`, and when
`needs.rig` is `rigctld`: `HAMSTACK_RIGCTLD` as `host:port`, same as `{rigctld}` (the first rig; several rigs will be a list in a later
schema). Plus each `config[].env`. Nothing else is promised.

## Plugin mode (no invented flags)

A plugin that also runs standalone must switch to plugin mode by itself when `HAMSTACK_PLUGIN_ID` is set: no login of
its own, no cookie, single rig from `{rigctld}`, no way to add arbitrary connection targets, framing allowed from the
same origin (`frame-ancestors 'self'`: the dashboard and `/plugin/<id>/` share one origin). The manifest never needs a
mode flag, and a plugin must not require one. A plugin that cannot do this today lists the gap in its README.

## What the proxy preserves (draft)

hamstack's reverse proxy forwards `Host` and `Origin` unchanged and upgrades WebSockets, so a plugin may check `Origin`
against `Host`. Browsers cannot add custom headers to a WebSocket, so for WebSocket endpoints the CSRF protection is an
`Origin` check plus a per-process random token the plugin embeds in the page it serves. If the person's radio stops being
controllable when hamstack or the plugin dies, only a hamstack-side watchdog can unkey it: that is a runtime item, not
something a plugin can do after it has died.

## Serving under a prefix

hamstack's reverse proxy keeps the prefix (`/plugin/<id>/api/health` reaches the program as `/plugin/<id>/api/health`
unless you say otherwise in a future schema). The plugin must therefore: serve its pages and API under `{base_path}`;
emit links, fetch URLs, WebSocket URLs and **cookie paths** relative to it; never use absolute `/api` or `Path=/`.
A single-page app built for `/` cannot know its prefix at build time (it is only known when hamstack starts it), so a
build-time `paths.base` is not enough. Either build with relative asset URLs (SvelteKit `paths.relative`, Vite
`base: './'`) and derive API and WebSocket URLs from `document.baseURI`, or have the Go/Node server rewrite
`index.html` at start-up with the prefix. If you rewrite it, compute any CSP hash of inline scripts from the rewritten HTML.

## Names and versions

`{tag}` is the git tag as written (`v1.2.3`); `{version}` is the tag without `tag_prefix` (`1.2.3`). `version_cmd` output may
be `1.2.3` or `name 1.2.3` or `v1.2.3`: hamstack takes the first semantic version in it. In plugin mode with one
rig, the rig's id is `hamstack` and a `?rig=` parameter, if your API has one, must default to it. A "client" for
unkey-on-last-client-leaves is a connected WebSocket (or other long-lived session).

## Stopping

On SIGTERM the plugin finishes within 5 seconds (then it is killed). A plugin that **transmits** must also unkey at start-up (a crash can leave the radio keyed), and must unkey the radio
(PTT off) before it exits, and also when its last client disconnects. hamstack will not start a transmitting plugin
that does not declare `needs.transmits`, and will refuse to run two things that key the same radio.

## Audio and voice plugins (draft)

A plugin that carries voice (browser microphone to the radio, radio audio to the browser) sets `needs.audio`,
`needs.transmits` and `ui.permissions: ["microphone"]`. hamstack's part: serve the dashboard over HTTPS, give the
plugin's frame `allow="microphone"`, and stop the rig's modem while the plugin holds the sound card. The plugin's
part: keep audio on WebSocket through the dashboard's proxy (UDP media such as WebRTC cannot pass a loopback-only
proxy), key the radio only while a person holds PTT, unkey on disconnect, page close, SIGTERM and a transmit time limit,
and offer the operator a station-identification reminder. See `best-practices.md`.

## Not supported yet

Windows and macOS plugins; non-GitHub sources; archives (use `raw_binary`); plugins that open serial ports; plugins
that are Go/Python libraries loaded into hamstack (never planned: plugins stay separate programs); fixed secrets.
