# hamgo and integrations: how hamstack, rigctlWeb and the plugins share code

Status: design, 2026-11-15. Nothing in this document has been built, moved, renamed, pushed or published. It replaces
`docs/modules.md` (a first-pass analysis) as the plan of record. Every claim about existing code cites the file it was
read from. Paths are relative to `~/Projects/`: `hamstack/`, `rigctlWeb/`, `hamstack-plugins/voice/`.

Everything described here is MIT, by the same owner, and carries the same statement as the rest of the work: free and
open source, no licence keys, no paywalls, no kill switches, ever (`rigctlWeb/README.md`, `ham-boilerplate/`). That
applies to hamgo and to every shared file, including the JavaScript.

Sections: 1 boundaries, 2 hamgo, 3 component contract, 4 plugin/integration/library, 5 rigctlWeb, 6 migration,
7 risks and questions, 8 the box below (placed first so it is read first).

## 8. What every agent must know (paste into each project's planning docs)

```
1. Shared code lives in ONE place: github.com/ATCUSA/hamgo (MIT, public, semver tags). Never copy a hamgo package into a program.
2. hamgo imports no host program, has no cgo, one dependency (coder/websocket, audiohttp only). Programs pin a hamgo TAG, never a branch.
3. Each component has ONE source of truth at any time (table in section 6). If it is not your repo today, do not edit it there.
4. rigctlWeb is an INTEGRATION (standalone app tied in through a versioned API), not a plugin. Plugins stay as specified in docs/plugins/.
5. hamstack keeps a light native radio card built on hamgo; everything richer is rigctlWeb's. Do not re-implement rigctlWeb panels in hamstack.
6. Nothing transmits without an explicit user action; every keyer has a hard limit and unkeys on exit, but ONLY if it keyed (rigctld is shared: a blind unkey cuts another program's over); the audio monitor never stops a modem or app.
7. System-wide changes (input volume is one) are explicit user actions, remembered so they can be restored, and ledger-recorded in hamstack.
8. Stop and ask the owner before: creating or pushing a GitHub repo, tagging, changing a module path, merging to main, anything on the Pi (for hamstack and plugin agents; rigctlWeb sessions deploy to the dev Pi under their own setup).
9. Tests use fakes for exec, files and rigctld, and fixtures captured from real systems. Never a real sound card, rigctld or radio in a test.
10. Secrets never go in URLs, command lines, environment values or logs. Pass a file path; the file is mode 0600.
```

## 1. Summary of the decision and the boundaries

The owner decided three things; this design implements them and does not reopen them:

1. One shared Go library, **hamgo** (`github.com/ATCUSA/hamgo`), for the whole endeavour.
2. One codebase per feature, so hamstack's audio (and everything else shared) has the same capabilities as rigctlWeb's
   even where the page looks different.
3. Plugins remain for many things; **rigctlWeb is an integration**, a standalone app tied into hamstack through an API.

| Lives in | What | Test for belonging there |
|---|---|---|
| **hamgo** | Protocol clients (rigctld, AGWPE, gpsd), receive-audio capture and its HTTP/WebSocket handler set and browser module, plugin/integration manifest and validator, small helpers (safetext, USB/ALSA discovery), test fakes | Useful to a program that is not hamstack; needs no installer, ledger, unit files, config store or UI framework |
| **hamstack** | Installer, catalog, profiles, config, ledger and uninstall, systemd unit generation, doctor, sudo prompts, jobs, TUI, the dashboard (pages, login, VNC relay), the plugin and integration runtimes, the light radio card | It installs, configures, supervises or records changes to the machine; or it is the dashboard product |
| **rigctlWeb** | The full radio control product: Svelte UI, views, search, integrations page, its config store and token login, the rig poller with optimistic state and benching (`rigctlWeb/internal/rig`), its TX safety rules | It is rigctlWeb's product behaviour or its UI |
| **plugins** | Separate programs that only make sense inside hamstack: voice, world clock, PSK map, net-ops dashboards | Has no standalone life and no login of its own; one job |

Three things are true today and drive the plan:

- **Three rigctld clients.** `hamstack/internal/rigctl/rigctl.go` (424 lines, no contexts, caps cached in the client,
  `Abort` on a second connection), `rigctlWeb/internal/rigctl/{client,rig,caps,caps_ext}.go` (1222 lines, contexts,
  Hamlib error text, 8 real `testdata/*.caps` fixtures), `hamstack-plugins/voice/rigctld.go` (43 lines, PTT only, one
  connection per command).
- **Two audio captures, already drifting.** `hamstack/internal/audiomon/stream.go` is identical to
  `rigctlWeb/internal/audio/stream.go` apart from the `package` line (checked with `diff`). Detection has diverged: rigctlWeb's `detect.go` has
  macOS/Windows and a richer `Report`; hamstack's `audiomon.go` added `Pick` (match the radio's card, never guess).
  rigctlWeb alone has the input-level `Mixer` (`level.go`), the settings and input endpoints
  (`internal/server/audio.go`) and the auto-level logic (`web/src/lib/autolevel.ts`).
- **The wire protocols differ.** rigctlWeb: `/ws/audio?rig=ID`, channel and device stored on the server, reconnect on
  "audio settings changed". hamstack: `/ws/audio/{rig}?channel=`, channel kept in `localStorage`
  (`hamstack/internal/web/audio.go`, `internal/web/page/rig.html` lines 137-196).

## 2. hamgo module design

### 2.1 Layout: one repository, one module

`github.com/ATCUSA/hamgo`, a single Go module with one `go.mod` and one tag series. Not sub-modules: sub-modules mean
one tag per directory (`audio/v0.3.0`), cross-module version pins inside the library, and several things to release
for one fix. The cost of a single module is that every package shares a version number; at v0 that is the right
trade. Go's module graph pruning means a program that imports only `hamgo/rigctl` does not build or need the source of
the one third-party dependency.

```
hamgo/
  go.mod                 module github.com/ATCUSA/hamgo; go directive = lowest version the code compiles with (CI checks it)
  LICENSE  README.md  CHANGELOG.md  DONATING.md
  safetext/              terminal-safe text
  rigctl/                rigctld client, typed commands, caps parser, testdata/*.caps
  rigctl/rigctltest/     fake rigctld (TCP) for tests in any program
  audio/                 detect, parse, CaptureCommand, Pick, Hub, Mixer
  audio/audiohttp/       net/http handler set + wire protocol; embeds audio/web
  audio/web/             hamgo-audio.js, hamgo-audio.d.ts, *.test.js (the browser module)
  manifest/              plugin and integration manifest: Parse, Validate
  integration/           what an integrated app serves back: info/health handlers, proxy-secret check, base path helper
  agw/  gps/  usbdev/  alsa/  hamlib/     second wave (2.3)
  internal/execx/        Runner and LookPath interfaces shared by audio, hamlib
  docs/                  one page per package + audio wire protocol + TX invariants
```

### 2.2 Package inventory, first wave (duplicated or needed by the integration today)

| Package | Purpose | Source today | Which version leads, and why | Dependencies | Platform |
|---|---|---|---|---|---|
| `safetext` | Strip escape sequences from text that came from outside (air, gpsd, USB names) | `hamstack/internal/safetext/safetext.go` | hamstack (only copy). Moves first because `agw`, `gps`, `usbdev` import it | stdlib (`strings`, `unicode`, `unicode/utf8`) | all |
| `rigctl` | rigctld client (extended protocol), typed get/set, `dump_caps` parser incl. extended caps | `rigctlWeb/internal/rigctl/*`; `hamstack/internal/rigctl/rigctl.go`; `voice/rigctld.go` | **rigctlWeb leads**: contexts on every call, `Reply` with keyed values, Hamlib error mapping, read-retry rules (`isRead`, `safeToRepeat`), caps for levels/funcs/parms/ext/modes/bandwidths, 8 real fixtures. Ported in from hamstack: `Abort` (drop the carrier from a second connection, `rigctl.go:401`), the `RFPOWER`/`RFPOWER_METER_WATTS` range helpers used for watts (`Caps.FullWatts`), and its test cases. Voice's "one short connection per command" becomes a documented usage (`NewClient`, one call, `Close`) | stdlib only (`bufio context errors fmt math net regexp sort strconv strings sync time`) | all |
| `rigctl/rigctltest` | Fake rigctld speaking `+f +m +t +T \dump_caps`, scripted replies, records what was sent | Each repo has its own TCP fake in tests (`hamstack/internal/rigctl/rigctl_test.go`, `rigctlWeb/internal/rigctl/rigctl_test.go:startDummy` and `internal/rig/rig_test.go`, `voice/helpers_test.go`) | New package written from the three, starting from rigctlWeb's `startDummy`; the session doing step 3 reads all three first and keeps every behaviour a test relies on | stdlib (`net`, `testing`) | all |
| `audio` | Find capture method and devices, build the capture command, `Pick` a radio's card, `Hub` (one process per key, only while listened to, fan-out, mono mix), `Mixer` (input volume get/set) | `rigctlWeb/internal/audio/{detect,parse,stream,level}.go`; `hamstack/internal/audiomon/{audiomon,stream}.go` | **rigctlWeb leads** (superset: macOS/Windows detection, `Tools`/`Server` in the report, `Mixer`). From hamstack: `Pick`/`squash` and its test, the "no pactl, use `ffmpeg -sources pulse`" test | stdlib only (`context encoding/binary encoding/json errors fmt io os os/exec regexp runtime strconv strings sync time`). Runs the system's own tools: `pw-record pw-cli pw-dump wpctl parec pactl arecord amixer ffmpeg` | Linux primary. macOS and Windows through ffmpeg exist but are marked untested in the code (`detect.go`) and stay marked so |
| `audio/audiohttp` | The handler set either server mounts (section 3) | `rigctlWeb/internal/server/audio.go` (350 lines); `hamstack/internal/web/audio.go` (relay only) | **rigctlWeb leads** (settings, validation, cached detection, input level with persisted restore). From hamstack: `friendlyAudioError` (the "busy" explanation) | stdlib + **`github.com/coder/websocket` v1.8.15** (its own `go.mod` has no requires; verified in the module cache). This is hamgo's only third-party dependency and only this package imports it; a test fails if any other package does | all |
| `audio/web` | Framework-neutral browser module: PCM decode, playback scheduling, meter, clipping, `measurePeak`, auto-level | `rigctlWeb/web/src/lib/{audio,autolevel}.ts` (pure, vitest-tested) and the logic inside `stores/{audio,autolevel}.svelte.ts`; hamstack's inline copy in `rig.html` | **rigctlWeb leads**. The Svelte `$state` wrappers stay in rigctlWeb | none (no npm packages at run time or test time; tests run with `node --test`) | evergreen browsers |
| `manifest` | Plugin and integration manifest types, `Parse` (unknown keys refused), `Validate` | `hamstack/internal/plugin/manifest.go` (260 lines) | hamstack (only copy). The validator stays the ground truth; `hamstack plugin validate` becomes a thin caller | stdlib (`encoding/json fmt regexp strings`) | all |
| `integration` | App-side helpers: the `/api/v1/integration` and health handlers, proxy-secret middleware, base path normalising and prefix-safe URL helpers | new; shaped by `docs/plugins/rigctlweb-changes.md` | new code, written once for rigctlWeb and reused by voice | stdlib (`crypto/subtle encoding/json net/http os strings`) | all |

### 2.3 Second wave (single copy today, clean, moves when its turn comes)

These are in hamstack only. They move because the owner's decision covers every reusable component, but after the
first wave, one package per step, and each only if the "boundary check" passes (no hamstack imports besides safetext).

| Package | Source | Deps | Note |
|---|---|---|---|
| `gps` | `hamstack/internal/gps/gps.go` (gpsd `?WATCH` client, `Grid` Maidenhead, `Describe`) | stdlib + `safetext` | Useful to net-ops and PSK-map plugins. Read only |
| `agw` | `hamstack/internal/agw/agw.go` (AGWPE frames, `Dial`, monitor, connected mode) | stdlib + `safetext` | `Connect`/`SendData` transmit: the package doc must say so, and callers own the explicit-action rule |
| `usbdev` | `hamstack/internal/usbdev/usbdev.go` (sysfs scan, serial+sound grouping, `Diff`) | stdlib + `safetext` | Linux only (reads `/sys` and `/dev`); build-tagged, with a stub that reports "unsupported" elsewhere. rigctlWeb's planned rigctld launcher (its ROADMAP "pick model + serial port") is the second consumer |
| `alsa` | `hamstack/internal/audio/audio.go` (`Cards`, `CardName`, `Resolve` from `/proc/asound/cards`) | stdlib | Linux only. Renamed from `audio` so it does not collide with the capture package |
| `hamlib` | `hamstack/internal/hamlib/hamlib.go` (`rigctld -l` model list, `Filter`, per-model caps) | stdlib (`os/exec`) | Second consumer: rigctlWeb's "favorite radio models" |

### 2.4 What does NOT move

- hamstack: `install`, `ledger`, `units`, `svc`, `doctor`, `catalog`, `profile`, `config`, `patcfg`, `apps`, `tui`,
  `web`, `distro`, `groups`, `netaccess`, `netinfo`, `versions`, `updates`. They are the installer/dashboard product or
  are tied to its catalog and ledger (`versions` imports `catalog` and `distro`; `svc` imports `ledger`).
- hamstack `internal/ax25` (kernel AX.25 state, `/etc/ax25/axports` planning): it is system set-up, not a client.
  The reusable packet client is `agw`.
- rigctlWeb `internal/rig` (poller, optimistic state with hold window, benching of slow reads, command executor),
  `internal/server`, `internal/config`, the views and the whole Svelte UI.
- **TX safety logic stays where it is for now.** rigctlWeb's 3-minute TX timeout (`rig.Config.TXTimeout`,
  `commands.go:armTXTimer`), 5 s tune cooldown and 30 s carrier cap (`commands.go:21-24`), unkey on shutdown
  (`Rig.Unkey`); the voice plugin's controller (`voice/controller.go`: watchdog, TOT, one transmitter); hamstack's tune
  cycle (`hamstack/cmd/hamstack/webrig.go:RigTune`). A shared `hamgo/txguard` is a possible later package, and it may
  only be extracted **together with its tests and a written invariant that it cannot be bypassed** (every key path
  goes through it; there is no exported way to key that skips the limit). Until then each program owns its own, and
  `hamgo/docs/tx-invariants.md` states the rules all three already follow (box above, line 6).
- Voice's transmit audio path (`voice/audio.go` playback, `web/worklet.js`): one consumer, safety-critical, stays.

### 2.5 Rules for the library

1. **No imports from host programs.** hamgo never imports `hamstack/...`, `rigctlweb/...` or a plugin. A CI test runs
   `go list -deps ./...` and fails on any module other than the standard library and `github.com/coder/websocket`, and
   on `coder/websocket` appearing outside `audio/audiohttp`.
2. **No cgo, no global state.** `CGO_ENABLED=0 go build ./...` in CI for linux/amd64, linux/arm64, linux/arm,
   darwin/arm64, windows/amd64. No package-level mutable variables, no `init` side effects.
3. **Interfaces for exec and files, fakes in tests.** Anything that runs a command takes a runner
   (`func(ctx, name string, args ...string) (string, error)`) and a `has(tool) bool`; anything that reads the system
   takes a root path or an `exists` function. This is already how `audio.detect` and `audio.Mixer` are built; the
   exported constructors (`Detect`, `NewMixer`) supply the real ones. `Hub` is the exception and stays as it is: it calls
   `exec.CommandContext` directly (`stream.go:Subscribe`) and its tests make the test binary act as the capture
   program (`stream_test.go:TestMain`), which exercises the real process handling.
4. **Tests use real captured output.** Fixtures are text captured from real systems, stored under `testdata/` with a
   line saying where and when (`ffmpeg 7.1 on Raspberry Pi OS`, `Hamlib 4.7.2 dump_caps FTDX10`). The ffmpeg 7
   `(none)` suffix once hid every device from a parser tested with an invented format; that fixture stays forever.
5. **Semver and tags.** Tags are `vMAJOR.MINOR.PATCH` on `main`. While v0: a minor bump may break an API and the
   CHANGELOG says exactly what to change; a patch never breaks. Wire protocols (audio, integration info) carry their
   own version number and are stricter than the Go API: additive only within a protocol version, even in v0.
6. **v0 stability promise.** "Pinned tags keep building forever; minor versions may change Go APIs with a migration
   note; wire protocol v1 does not break." v1.0.0 is cut when all three programs have run on the same hamgo minor for
   a release without an API change.
7. **Consumers pin tags.** `require github.com/ATCUSA/hamgo vX.Y.Z` only. No `replace` line and no pseudo-version is
   ever committed in a consumer. `go build` on a clean machine with only the public proxy must work for every commit
   on a consumer's main branch; rigctlWeb stays one static binary.
8. **Local development before and between tags: `go.work`, never committed, opt-in.** The file lives in its own
   folder, `~/Projects/.hamgo-work/go.work`, listing `../hamgo ../hamstack ../rigctlWeb ../hamstack-plugins/voice`,
   and is used only by a session that sets `GOWORK=$HOME/Projects/.hamgo-work/go.work`. It must **not** be placed in
   `~/Projects/` itself: Go applies a `go.work` found in any parent folder, so it would silently change every build in
   every project there, and `~/Projects/hamstack-public` has the same module path as hamstack will have. Workspace
   modules resolve imports without a `require`, so a consumer branch can be built and tested against unpublished
   hamgo. Such a branch is not merged until the tag it needs exists; CI and release builds use `GOWORK=off`.
9. **Offline cache.** Do not promise fully offline Go builds: after the first consumer step the Go build needs hamgo from the module cache or a proxy, exactly as `coder/websocket` is needed today (vendoring the JS only keeps the pnpm/vite build offline). hamstack's module cache is used offline. `coder/websocket@v1.8.15` and `golang.org/x/net@v0.62.0`
   are already in it. A published hamgo tag needs one online `go mod download github.com/ATCUSA/hamgo@vX.Y.Z` (an owner
   step, listed in section 6); until then the workspace file covers local builds.
10. **Docs.** Each package has a package comment that says what it does and whether anything in it can transmit, and
    a runnable `Example`.

### 2.6 hamstack's module path

`docs/modules.md` and the task brief say hamstack's module must be renamed from `hamstack` to
`github.com/ATCUSA/hamstack`. The repository says something more specific: `origin/main`, tag `v0.1.0` and tag `v0.2.0`
**already use `module github.com/ATCUSA/hamstack`** (`git show origin/main:go.mod`). Only the local branch
`local-work-before-v0.2.0` (69 commits ahead of `origin/main`, 10 behind) uses `module hamstack`, in 330 import lines
across 100 files. So this is a reconciliation with published history, not a first rename.

- **What:** one mechanical commit: `go.mod` module line plus every `"hamstack/internal/...` import becomes
  `"github.com/ATCUSA/hamstack/internal/...`; `go build ./... && go test ./...`; nothing else in that commit.
- **When:** before the first hamgo import lands in hamstack and before the branch is merged with `origin/main`, at a
  moment when no other session has uncommitted work in hamstack (the commit touches 100 files). hamgo does not depend
  on it, so it does not block creating hamgo.
- **Owner gate:** yes (section 6). The branch also lacks the `LICENSE` file that `origin/main` has; the merge restores it.

## 3. The component contract for frontends

A shared feature is delivered as four pieces with one owner each. The audio monitor is the worked example and the
template for later features.

| Piece | Where | Who provides it |
|---|---|---|
| Core Go package (no HTTP, no config) | `hamgo/audio` | hamgo |
| Handler set: `net/http` handlers with injected interfaces | `hamgo/audio/audiohttp` | hamgo; the host mounts it and supplies auth, routing, config persistence and rig lookup |
| Wire protocol document | `hamgo/docs/audio-protocol.md`, version 1 | hamgo |
| Browser module: plain ES module, no build step | `hamgo/audio/web/hamgo-audio.js` (+ `.d.ts`) | hamgo; each UI binds it to its own widgets |
| The widgets, layout, wording, help text | each program | hamstack (`internal/web/page/rig.html`), rigctlWeb (`AudioPanel.svelte`) |

### 3.1 Go: the handler set

```go
package audiohttp

// Rig is what the audio feature needs to know about a radio. The host decides what a rig is.
type Rig struct {
    ID       string
    CardHint string   // sound card name to match with audio.Pick; "" = no hint
    SharedBy []string // names of programs using this input now (a modem, an app); shown as a warning, never acted on
}

type Rigs interface {
    Lookup(r *http.Request, id string) (Rig, bool)
}

// Settings is one rig's persisted audio state. The host stores it wherever it stores config.
type Settings struct {
    Enabled       bool     `json:"enabled"`
    Device        string   `json:"device"`  // "" = choose (CardHint via Pick, else first device)
    Channel       string   `json:"channel"` // left | right | mix
    RestoreDevice string   `json:"-"`       // never set from the API
    RestoreVolume *float64 `json:"-"`
}

type Store interface {
    Load(rigID string) (Settings, error)
    Update(rigID string, fn func(*Settings) error) error // atomic read-modify-write
}

type Options struct {
    Rigs   Rigs
    Store  Store
    Hub    *audio.Hub                          // nil = a new one
    Mixer  audio.InputMixer                    // nil = audio.NewMixer(); an interface so tests fake it
    Detect func(context.Context) audio.Report  // nil = audio.Detect; cached 20 s as today
    RigID  func(*http.Request) string          // how the host's routes carry the rig id (path value or ?rig=)
    Log    *slog.Logger
    // OnInputChange runs after the input volume really changed (old, new). hamstack records it in the ledger here.
    OnInputChange func(rig Rig, device string, old, new float64)
}

type Handlers struct {
    Settings http.Handler // GET, PUT   per-rig settings + detection report + listener count
    Input    http.Handler // GET, PUT   input volume: read, set, restore
    Stream   http.Handler // GET (WebSocket)
    Script   http.Handler // GET        hamgo-audio.js from the embedded file, with a content hash ETag (arrives with the JS, step 13)
}

func New(o Options) *Handlers
func Assets() fs.FS // audio/web, for hosts that bundle instead of serving
```

Rules of the contract:

- **The host owns auth and routing.** hamgo never looks at cookies, tokens or sessions. The host wraps each handler in
  its own middleware (`s.auth(...)` in `hamstack/internal/web/server.go`, `s.requireAuth(...)` in
  `rigctlWeb/internal/server/server.go`) and its own CSRF rule for the PUTs (hamstack: same-origin check plus the `X-Hamstack: 1` header, `s.action`;
  rigctlWeb: a `SameSite=Strict` cookie plus `Content-Type: application/json` required by `server.go:decode`, which
  moves into the handler set as a rule for every host). The handlers themselves always enforce same-origin on the WebSocket
  (`websocket.Accept` default) and a body size limit; a host cannot turn those off.
- **The host owns paths.** The protocol fixes messages and JSON shapes, not URLs. rigctlWeb keeps
  `/api/rigs/{id}/audio`, `/api/rigs/{id}/audio/input`, `/ws/audio?rig=`; hamstack mounts
  `/api/rig/{id}/audio`, `/api/rig/{id}/audio/input`, `/ws/audio/{rig}` and `/static/hamgo/audio.js`. The browser
  module is constructed with the four URLs.
- **WebSocket library.** One confined dependency instead of an injected upgrader: an upgrader interface would have to
  abstract binary and text writes, close codes and read cancellation, which is the whole library. hamstack keeps
  `golang.org/x/net/websocket` for its VNC relay (`server.go:ws`) and stops using it for audio.
- **Device selection never guesses when it has a hint.** `Settings.Device` set: it must be in the current detection
  report (re-checked before it reaches a command line, as `handleAudioWS` does today). Empty with `CardHint`:
  `audio.Pick`, and an error that names the card if nothing matches (hamstack's rule, `cmd/hamstack/listen.go`). Empty
  without a hint: first device (rigctlWeb's standalone behaviour, where the user enabled the monitor explicitly).
- **Defaults are the host's.** rigctlWeb's `Store` returns `Enabled: false` until the user turns the monitor on (opt-in
  per rig, as now). hamstack's `Store` returns `Enabled: true` for a rig that has a sound card configured, because the
  dashboard already sits behind its login and Listen is one click today. Same code, different default, both stored.

### 3.2 Wire protocol, audio version 1

rigctlWeb's protocol is adopted unchanged; hamstack changes (it drops `?channel=`; the channel becomes a server-side
setting, so every browser hears the same side and a settings change reaches open listeners).

```
GET  settings        -> {"config":{"enabled":b,"device":s,"channel":"left|right|mix"},
                         "available":{"os","arch","tools":{},"method","devices":[{"id","name"}],"notes":[],"server":{}},
                         "listeners":n, "protocol":1}
PUT  settings  <- config -> same view. 400 invalid channel | unknown audio device | capture not available.
                         Saving stops the capture with reason "audio settings changed".
GET  input           -> {"supported":b,"reason":s,"volume":0..1,"original":0..1?,"sharedBy":[s]}
PUT  input     <- {"volume":0.05..1} | {"restore":true} -> same view.
                         400 out of range, 409 no input / nothing to restore, 501 unsupported (reason in body), 502 tool failed.
WS   stream          -> text  {"type":"audio","rate":12000,"channels":1,"format":"s16le","channel":"mix","protocol":1}
                        binary frames: 480 samples (40 ms) mono s16le
                        text  {"error":"..."} then close. "audio settings changed" means: reconnect.
                        403 audio is off for this rig; 409 audio device not found (before the upgrade).
```

Additive fields (`protocol`, `sharedBy`) are the only differences from what `rigctlWeb/internal/server/audio.go` sends
today. Constants stay as in `stream.go`: 12 kHz, stereo capture, 480-sample frames, 4 listeners per rig, a slow
listener loses its oldest frame.

### 3.3 Browser module

`hamgo-audio.js` is one plain ES module: no framework, no bundler needed, no dependencies. CSP-safe by construction:
no inline script, no `eval`, no `blob:` or `data:` workers, no AudioWorklet (playback is scheduled
`AudioBufferSourceNode`s exactly as both programs do now), so it runs under rigctlWeb's
`script-src 'self'`/`connect-src 'self'` policy (`server.go:csp`).

```js
export const FLOOR_DB, TARGET_DB, HOT_DB, MIN_VOLUME;
export function decodePcm(buf)            // {samples: Float32Array, peak}
export function toDb(peak), isClipping(peak)
export function judge(peakDb), planVolume(peakDb, volume, targetDb), responded(beforeDb, afterDb, volBefore, volAfter)
export class Monitor extends EventTarget {  // one per rig being listened to
  constructor({ streamUrl, storageKey })    // streamUrl: string | (rigId) => string
  status; error; db; clipped; volume;       // plain properties; a "change" event fires when any changes
  start(rigId)  stop()  setVolume(v)  measurePeak(ms, onProgress)
}
export class AutoLevel extends EventTarget {
  constructor({ monitor, getInput, setInput }) // the two functions call the host's input endpoint with the host's headers
  input; phase; message; progress; seconds;
  refresh(rigId)  setVolume(rigId, v)  restore(rigId)  run(rigId)  cancel()
}
```

- rigctlWeb: `stores/audio.svelte.ts` and `stores/autolevel.svelte.ts` shrink to wrappers that copy the properties
  into `$state` on each `change` event. `AudioPanel.svelte` does not change.
- hamstack: the inline `listen()` in `rig.html` is deleted; the radio card script imports the module with
  `<script type="module" src="/static/...">` and binds `change` to its own meter, button and note elements.
- Every user-visible sentence the module produces ("The audio connection closed.", the auto-level messages) is in one
  exported `messages` object so a host can reword without forking logic.

**Delivery: the JS ships inside the Go module (`//go:embed` in `audio/audiohttp`). No npm package.**

- hamstack serves it from the embed (`Handlers.Script`). hamstack has no static JS route today and its pages use
  inline scripts with no CSP (`server.go:securityHeaders` sets only HSTS, nosniff, Referrer-Policy and
  `X-Frame-Options: DENY`); adding the route is part of the hamstack audio step, and moving the radio card's script to
  a served file is recommended in the same step so a CSP becomes possible later.
- rigctlWeb bundles it with vite. To keep "builds offline from a clean checkout" true, the file and its `.d.ts` are
  **vendored and committed** at `web/src/lib/vendor/hamgo-audio.{js,d.ts}`, copied by a script from the pinned module
  (`go list -m -f '{{.Dir}}' github.com/ATCUSA/hamgo`). A Go test in rigctlWeb compares the vendored bytes with
  `audiohttp.Assets()` and fails on any difference, so the copy cannot drift from the pinned tag and nobody edits it
  in place. An npm package was rejected: it adds a registry, a second version number and a publish step, and breaks
  the offline build.
- Tests: the pure-function tests now in `rigctlWeb/web/src/lib/{audio,autolevel}.test.ts` move to
  `hamgo/audio/web/*.test.js` and run with `node --test` (no packages to install). hamgo's Go tests do not need node;
  a separate CI job runs the JS tests. rigctlWeb keeps vitest tests for its wrappers only.

### 3.4 Audio feature parity list (everything rigctlWeb's monitor does today) and owners

| # | Capability | Verified in | Owner after the move |
|---|---|---|---|
| 1 | Detect method: PipeWire, PulseAudio, ALSA; macOS avfoundation, Windows dshow via ffmpeg; tools found, servers answering, notes | `audio/detect.go` | hamgo/audio |
| 2 | Device listing from `pactl`, `ffmpeg -sources pulse` (no pactl), `arecord -l`; monitors excluded | `audio/parse.go` | hamgo/audio |
| 3 | Capture commands: quiet, stdout only, no files | `detect.go:CaptureCommand` | hamgo/audio |
| 4 | One capture process per rig, only while someone listens; stops with the last listener | `stream.go:Hub` | hamgo/audio |
| 5 | Up to 4 listeners; slow listener drops frames, never stalls capture | `stream.go:pump` | hamgo/audio |
| 6 | Left / right / mix, chosen per rig, applied per listener | `stream.go:toMono` | hamgo/audio |
| 7 | Capture tool's last error text reaches the listener | `stream.go:tailBuf` | hamgo/audio; busy-card wording from hamstack |
| 8 | Per-rig settings enabled/device/channel with validation; detection cached 20 s, fresh on save | `server/audio.go` | hamgo/audiohttp; storage by host |
| 9 | Settings change stops capture; listeners reconnect by themselves | `audio.go:handlePutAudio`, `audio.svelte.ts:#control` | audiohttp + JS |
| 10 | Input volume: read, set 5-100 %, never above 100 %; wpctl+pw-dump, pactl, amixer; "unsupported" with a reason | `audio/level.go` | hamgo/audio |
| 11 | Original volume remembered before the first change, survives restart, one-click restore | `audio.go:handlePutAudioInput`, `config.AudioConfig` | audiohttp logic; host persists |
| 12 | Playback: 12 kHz context with fallback, 120 ms priming, re-prime when dry, skip when 0.5 s behind | `audio.svelte.ts:#play` | hamgo JS |
| 13 | Volume in this browser, remembered | `audio.svelte.ts:setVolume` | hamgo JS (storage key from host) |
| 14 | Level meter in dBFS, floor -60, instant rise, slow fall, 50 ms tick | `audio.ts`, `#tick` | hamgo JS; drawing by host |
| 15 | Clipping flag at 0.985 full scale, held 1.5 s | `audio.ts:isClipping` | hamgo JS |
| 16 | `measurePeak(ms)` with progress | `audio.svelte.ts` | hamgo JS |
| 17 | Auto-adjust: listen 2-5 s, lower toward -6 dBFS, up to 3 steps, verify the level followed, put the volume back and say "clipping is upstream" if not, cancel | `autolevel.ts`, `autolevel.svelte.ts:run` | hamgo JS |
| 18 | Manual input slider, debounced 200 ms | `autolevel.svelte.ts:setVolume` | hamgo JS |
| 19 | Status idle/connecting/listening/error; stop on rig change (stopping on page hide is a host choice: hamstack stops, rigctlWeb keeps listening in the background) | stores, `rig.html` | hamgo JS; host calls `stop()` |
| 20 | `rigctlweb audio detect` command | `cmd/rigctlweb/audio.go` | each program's CLI, calling `audio.Detect` |
| 21 | Match the radio's own card, never guess | `hamstack audiomon.Pick` | hamgo/audio (rigctlWeb gains it) |

hamstack reaches parity when its radio card exposes 8-19 through its own widgets. Features still planned in rigctlWeb
(optional waterfall, an `audio setup|revert` helper, macOS/Windows verification; none exists in the code today) are
written once, in hamgo, after the move.

## 4. Plugin vs integration vs library

### 4.1 Decision criteria

Ask in this order; the first "yes" decides.

1. **Is it code with no process and no page of its own, useful to more than one program?** Library: hamgo.
2. **Does it install packages, write unit files, need sudo, or record changes to the machine?** Native hamstack.
3. **Is it somebody else's program that hamstack installs and configures (Pat, WSJT-X, Direwolf, ardopcf)?** Managed
   app (the existing catalog and `internal/apps`). Not a plugin, not an integration.
4. **Does it have a life without hamstack: its own users, its own login, its own releases, people who run it on a
   machine with no hamstack?** Integration.
5. **Otherwise** (it only makes sense inside the dashboard, has no login, does one job): plugin.

| | Plugin | Integration |
|---|---|---|
| Runs without hamstack | no | yes, fully |
| Own authentication | none (spec: "no login of its own") | yes standalone; delegated to hamstack when integrated |
| How hamstack learns of it | any author's `hamstack-plugin.json` | first-party entry compiled into hamstack's catalog, plus a live handshake |
| What hamstack depends on | the manifest contract | a small versioned API (`/api/v1/integration`, health) |
| URL in the dashboard | `/plugin/<id>/` | `/integration/<id>/` |
| Can use hamgo | yes | yes |
| May see more than one rig | no (first rig, schema 1) | yes (all rigs hamstack allows it) |

### 4.2 Each known thing

| Thing | Verdict | Why |
|---|---|---|
| **rigctlWeb** | Integration | Standalone product with token login, config, multi-rig, its own roadmap; also part of hamstack |
| **voice** | Plugin | No standalone life: it needs hamstack's HTTPS (microphone), login, modem stopping and rigctld. It transmits, so it is the plugin that proves `needs.transmits`. Uses `hamgo/rigctl` and `hamgo/integration` helpers. Open question 6 covers its overlap with rigctlWeb's planned browser-mic transmit |
| **worldclock** | Plugin | Pure display, no radio: first test of the plugin runtime, as `docs/plugins/ideas.md` says |
| **psk-map** | Plugin | Network only; wants call sign and grid from hamstack (`/api/v1/station`, section 4.4) |
| **net-ops / SHARES / MARS / CAP dashboards** | Plugin(s) | Forms, schedules, check-in logs: hamstack-only pages with their own data folder. If one grows a server shared by several stations it becomes an integration then, not before |
| **small-login** | Not a plugin | A plugin sits behind hamstack's login; a login provider cannot. If it is "a login hamstack and integrated apps accept", it is a native hamstack component plus a `hamgo` package; if it is a standalone identity service, it is an integration. Needs the owner's clarification first (open question 5) |
| rigctld multiplexer (rigctlWeb ROADMAP) | Library + whoever runs it | The proxy logic is a `hamgo` package; the process belongs to whichever product supervises rigctld (hamstack when present) |
| Home Assistant / MQTT / Prometheus export | Integration, outward | An external system reading hamstack's `/api/v1`; nothing embedded |
| Logging/ADIF helper, band-plan viewer | Plugin | One job, inside the dashboard |

### 4.3 What an integration is, concretely

**Discovery.** hamstack ships a descriptor for each first-party integration in its own catalog (`internal/catalog`),
using the manifest format with `"kind": "integration"` (schema 2, section 4.7). There is no remote registry and no
scanning of ports. A descriptor says where releases come from, how to start the program, and the minimum integration
API version. At run time hamstack confirms with a handshake: `GET {base}/api/v1/integration` (4.5).

**Three modes, chosen by the user on the Apps page:**

| Mode | What hamstack does | Login |
|---|---|---|
| **Managed** (default) | Installs the release (sha256 verified, as plugins), runs its own instance as a user service, proxies it | hamstack's login; none in the app |
| **Linked** | The user already runs the app (own service, own port, maybe another machine). hamstack stores the URL and shows a link that opens in a new tab | the app's own login; hamstack is not involved |
| **Off** | Nothing | - |

hamstack never adopts, edits or stops an instance it did not start. A user's standalone rigctlWeb (its packaged
`rigctlweb.service`, its `~/.config` file) is left alone; Managed mode uses a separate config folder inside hamstack's
area, so uninstalling hamstack leaves the standalone one exactly as it was.

**Install, start, stop (Managed).**

- Binary from the GitHub release named by the descriptor, checksum verified with the existing release code
  (`internal/versions.ReleaseAssets`), placed under hamstack's own data folder.
- A user unit `hamstack-int-<id>.service` generated by `internal/units` alongside the rig and modem units, started
  after the rig services it uses. The unit file and the installed binary are **ledger entries**, so
  `hamstack uninstall` removes them; the integration's data and config folders are app data and are never deleted
  without asking (same rule as `docs/app-folders.md`).
- Start/stop/restart/logs reuse the existing service controls (`POST /api/service/{name}/{verb}`,
  `GET /api/logs/{name}`), so every button already shows busy and result.
- If the integration is allowed to transmit on a rig, its unit gets an `ExecStopPost` unkey hook like the ardopcf unit
  (`internal/units/units.go:311`, `cmd/hamstack/ardopcf.go:runUnkey`), but **conditional**: it unkeys only when the app's
  own "I keyed" marker file exists (see the unkey rule below), never blindly, because rigctld is shared with Pat and
  the modems. That is the watchdog the plugin spec says only hamstack can provide: if the app dies keyed, the radio is
  unkeyed, and if it never keyed, nothing is touched. (ardopcf's hook may stay unconditional: ardopcf is the program
  that keys through that rig while it runs.)

  **The unkey rule (all keyers, rigctlWeb and plugins):** a program unkeys only if it keyed. It keeps a small marker
  file (in its data dir, cleared when it unkeys) holding the rig id and time it last keyed. On start-up, on shutdown and
  when the last controlling WebSocket leaves, it sends `set_ptt 0` only when the marker says it is keyed, and clears the
  marker afterwards. A crash while keyed leaves the marker, so the next start (or hamstack's `ExecStopPost`) unkeys.
  `Rig.Unkey` in rigctlWeb (`internal/rig/commands.go:419`) used to send `set_ptt 0` unconditionally.

  **Done in rigctlWeb (step 8, commit c44cda9, local):** marker file `<config dir>/state/keyed-<rig id>` (today
  `~/.config/rigctlweb/state/`; in integration mode it moves under `HAMSTACK_DATA_DIR`, step 15, so hamstack must not
  hard-code the path yet), content `<pid> <unix time>`, mode 0600. It is written before key-up and removed once the radio
  is confirmed unkeyed (3 tries, 150 ms apart; if all fail the marker stays). Start-up recovery: a marker younger than 15
  minutes and the radio reporting PTT on means unkey and remove; an older marker is ignored and removed; no marker means
  nothing is ever sent. Shutdown unkeys first, 5 s cap, only rigs it keyed; a disconnect unkeys only if that WebSocket
  keyed. **Consequence for hamstack's `ExecStopPost` hook:** the app removes the marker itself after a clean unkey, so the
  hook only matters after a crash or kill; it should unkey only when the marker exists AND is under 15 minutes old, the
  same test the app applies. Other keyers (voice) should adopt the same marker format so one hook serves all.

**Authentication between hamstack's login and the app.**

- The app listens on `127.0.0.1` on a port hamstack picks and is reached only through hamstack's reverse proxy at
  `/integration/<id>/`, behind `s.auth`. hamstack has no HTTP reverse proxy today (only the VNC WebSocket relay in
  `server.go:ws`); one `httputil.ReverseProxy` with WebSocket upgrade serves both plugins and integrations and is new
  work.
- Loopback alone is not authentication: any local process could reach the port. So hamstack creates a random
  256-bit **proxy secret** per integration in `{config_dir}/proxy-secret` (mode 0600), passes only its **path**
  (`HAMSTACK_PROXY_SECRET_FILE`), and the proxy adds `X-Hamstack-Proxy-Auth: <secret>` to every forwarded request,
  including WebSocket upgrades (a proxy can add headers to an upgrade; a browser cannot). The app, in integration
  mode, refuses any request without the right value (constant-time compare, `hamgo/integration.RequireProxy`), offers
  no login page and sets no cookie. The proxy strips that header and hamstack's session cookie from what the browser
  sent, so the app never sees hamstack's cookie and a browser can never supply the secret.
- The secret is never in a URL, a command line, an environment value or a log line. There is no token hand-off, no
  redirect with a ticket, no shared cookie.
- Honest limit: rigctld itself has no authentication, so a local process can already key the radio through it. The
  proxy secret keeps the app from being a second unauthenticated door and protects the app's own settings; it does
  not make loopback safe from a hostile local user.
- Linked mode has no SSO by design.

**Appearance in the dashboard.** Three levels, an integration uses the lowest that does the job:

1. **Link** (Linked mode): a card with the URL, opens in a new tab.
2. **Proxied page** (Managed): the app's own UI at `/integration/<id>/`, shown inside the dashboard shell in a frame
   and openable full-window. Requires in hamstack: exempt `/integration/` and `/plugin/` responses from the blanket
   `X-Frame-Options: DENY` (`server.go:securityHeaders`); the app sends `frame-ancestors 'self'`. Requires in the app:
   base path support.
3. **Native panel driven by the app's API**: a hamstack page calls the app's versioned API through the proxy. Not
   used for rigctlWeb, because shared features come from hamgo instead; reserved for apps hamgo cannot serve.

**Sharing the radio safely.**

- One rigctld per rig, owned by hamstack's rig service; the serial port is opened by nothing else. An integration is a
  rigctld client, exactly as plugins are (`needs.rig: "rigctld"`).
- hamstack tells the app which rigs it may use: `HAMSTACK_RIGS` (rigctlWeb keeps its views, theme and audio settings in its own config file in its data dir, `HAMSTACK_DATA_DIR`, even when the rigs come from this variable), entries separated by `;`, each `id=host:port` or
  `id=host:port,ro` (for example `ftdx10=127.0.0.1:4532;ic705=127.0.0.1:4533,ro`). The ids are hamstack's rig ids;
  `ro` marks a rig the app must treat as read-only.
- **Who may key.** Per rig, hamstack keeps a setting "Allow <app> to transmit on this radio", off by default, changed
  only by the owner in the dashboard. Off means the rig is passed with `ro` (rigctlWeb already has
  `rig.Config.ReadOnly`, which refuses every set command, `commands.go:Exec`). This is cooperative, not enforced:
  rigctld cannot refuse a client. The proposed multiplexer is the only way to enforce it and is out of scope here.
- **One PTT owner at a time is advice, not a lock, in v1.** hamstack shows on the radio card which things are allowed
  to key that rig (modem, tune, integration) and warns when a modem is running and the integration is also allowed.
  It does not stop either.
- **Limits stay in each keyer.** rigctlWeb's TX timeout, tune cooldown and carrier cap apply in integration mode
  unchanged; hamstack's tune remains gated by the owner's enable switch (`RigState.TuneOn`). Both poll the same CAT
  link, so the integration contract repeats the plugin rule: about one read per second per meter set.

### 4.4 The API hamstack exposes (versioned, built from what exists)

Today's routes in `internal/web/server.go` are unversioned and shaped for hamstack's own pages. They stay as they are
for the pages. A stable, read-only surface is added under `/api/v1/`, served by the same handlers' data:

| Endpoint | Returns | Built from |
|---|---|---|
| `GET /api/v1/info` | hamstack version, API versions (`["v1"]`), base URL | new, trivial |
| `GET /api/v1/rigs` | id, name, online, rigctld `host:port`, sound card hint, `transmitAllowed` for the caller | `RigControl.RigStates`, config |
| `GET /api/v1/rigs/{id}/state` | the `RigState` JSON (`internal/web/rig.go`) | `GET /api/rigs` |
| `GET /api/v1/services` | name, what, state per service | `Backend.State` |
| `GET /api/v1/station` | call sign, grid, position source | config stations, `gps` |
| `GET /api/v1/integrations` | id, mode, version, state, capabilities of each integration | new |

- **Callers in v1:** pages served under `/integration/<id>/` or `/plugin/<id>/`, same origin, with the user's session
  (the browser sends hamstack's cookie to hamstack's own `/api/v1`; the proxy still strips it before the app).
  Everything is GET. State-changing calls stay on the unversioned routes and are not part of the integration surface.
  That is a convention, not a boundary: a page under `/plugin/` or `/integration/` shares the dashboard's origin, so
  its script can call any dashboard route and set `X-Hamstack: 1` itself (see Risks).
- **Backend-to-hamstack calls** are not in v1. An integration's server gets what it needs at start from the
  environment (`HAMSTACK_INTEGRATION_ID`, `HAMSTACK_BASE_PATH`, `HAMSTACK_RIGS`, `HAMSTACK_DATA_DIR`,
  `HAMSTACK_CONFIG_DIR`, `HAMSTACK_PROXY_SECRET_FILE`); hamstack restarts it when rigs change. A token-authenticated
  loopback API is deferred until something needs it (open question 7).
- **Versioning:** the path carries the major (`/api/v1`). Within v1 only additions; clients ignore unknown fields. A
  breaking change is `/api/v2`, with v1 kept for at least two hamstack minor releases. `GET /api/v1/info` lists what
  is served.

### 4.5 The API an integrated app exposes back

hamstack depends on exactly this, and on nothing else of the app's API:

| Endpoint | Contract |
|---|---|
| `GET {base}/api/health` | 200 when the app can serve. Not tied to the radio. No proxy secret required (so the unit's readiness check is simple); returns no data beyond `{"status":"ok"}` |
| `GET {base}/api/v1/integration` | `{"name","version","api":["v1"],"mode":"hamstack"\|"standalone","capabilities":["rig.control","audio.monitor",...],"transmits":bool,"rigs":[{"id","readOnly"}]}` |
| `-version` | one line with a semantic version (as plugins) |
| SIGTERM | unkey first, then stop; exit within 5 s |

`hamgo/integration` implements the info and health handlers and the proxy check so every app answers identically.

rigctlWeb's existing REST and WebSocket API (`/api/rigs`, `/api/views`, `/ws` ...) is unversioned and changes often.
**hamstack must not call it.** Its UI is proxied whole, so it remains private to rigctlWeb and free to change. If a
part is ever needed by hamstack it is first published under `/api/v1/` with the same additive-only policy.

### 4.6 What changes and what stays in the plugin spec

Stays: plugins are separate programs; GitHub release plus checksum; loopback; base path; SIGTERM; no serial; the
validator is the ground truth; the safety rules in `best-practices.md`; schema 1 manifests remain valid.

Changes: rigctlWeb is no longer "the first plugin"; the proxy secret applies to plugins too; a schema 2 adds `kind`.

### 4.7 Proposed edits to `docs/plugins/` (a list; none applied)

1. `README.md`: add a "Plugin or integration?" paragraph linking here; remove the `rigctlweb-changes.md` row's words
   "first real plugin candidate"; name world clock as the first plugin.
2. `spec.md`: add schema 2 with `kind: "plugin" | "integration"` (default `plugin`), `ui.auth: "delegated"` and
   `integration.api: "v1"` for integrations; document `HAMSTACK_PROXY_SECRET_FILE` and the
   `X-Hamstack-Proxy-Auth` header for both kinds; document `HAMSTACK_RIGS` (integrations) beside `HAMSTACK_RIGCTLD`
   (plugins, first rig); state that the unit of a transmitting plugin gets the unkey `ExecStopPost`.
3. `spec.md` "Not supported yet": the line "plugins that are Go/Python libraries loaded into hamstack (never planned)"
   gains "shared code is a library in hamgo, which a plugin may import".
4. `guide.md`: replace rigctlWeb as the worked example with the world clock (no radio) and voice (transmits); add a
   short "use hamgo" section (`hamgo/rigctl`, `rigctl/rigctltest`, `hamgo/integration`, `hamgo/manifest`).
5. `best-practices.md`: "Test with a fake rigctld" points at `hamgo/rigctl/rigctltest`; add the proxy-secret rule.
6. `ai-prompt.md`: add `HAMSTACK_PROXY_SECRET_FILE`; tell the assistant to import hamgo instead of writing a rigctld
   client; say validation is `hamgo/manifest` (same validator, same command).
7. `rigctlweb-changes.md`: move to `docs/integrations/rigctlweb.md`, retitled "What rigctlWeb needs to be a hamstack
   integration", content updated per section 5.
8. `examples/rigctlweb.json`: move to `docs/integrations/examples/` as a schema 2 descriptor; add
   `examples/worldclock.json` as the plugin sample. (The current example also uses `-base-path` and `-rig`, flags its
   own notes say do not exist yet.)
9. `ideas.md`: add a "kind" column using 4.2; mark small-login "not a plugin, needs clarification".
10. `internal/plugin/manifest.go` (code, when the step comes): add `integration` to the reserved ids; the package
    moves to `hamgo/manifest`.

## 5. rigctlWeb as the first integration

### 5.1 What hamstack does

1. Catalog descriptor for rigctlWeb (kind integration, `ATCUSA/rigctlweb`, `rigctlweb-{tag}-{os}-{arch}`,
   `sha256sums.txt`: its release assets already have this shape).
2. Integration runtime: install, `hamstack-int-rigctlweb.service`, ledger entries, port choice, proxy secret file,
   environment, health wait, handshake, the unkey hook when transmit is allowed.
3. Reverse proxy at `/integration/rigctlweb/` behind the login, WebSocket upgrade, header stripping/adding, frame
   header exemption.
4. Apps page: Managed / Linked / Off, version shown from the handshake, start/stop/logs with feedback.
5. Radio card: an "Open full radio control" link to `/integration/rigctlweb/?rig=<id>` when the integration runs;
   per-rig "Allow rigctlWeb to transmit" switch.
6. `/api/v1/` read-only surface (4.4).

### 5.2 What rigctlWeb must do

From `docs/plugins/rigctlweb-changes.md`, restated for integration mode. Verified against the code on 2026-11-15:
none of these exist yet (`cmd/rigctlweb/main.go` has only `-config -listen -rig -debug -dev-no-auth -show-token
-version`).

| # | Change | Where today |
|---|---|---|
| 1 | **Base path** (feasible in the static SvelteKit SPA with relative asset paths, `paths.relative`; the CSP hash is computed from the served `index.html` at runtime so no rewrite is needed; but every absolute `/api`, `/ws`, `/ws/audio` URL and every in-app link must derive from the page base; prove it first on the world-clock plugin): `-base-path` / `HAMSTACK_BASE_PATH`; all routes under it; SPA built with relative asset URLs and API/WS URLs derived from `document.baseURI`; CSP hash computed from the served HTML | absolute `/api`, `/ws`, `/ws/audio` in `server.go:Handler`, `api.ts`, `audio.svelte.ts` |
| 2 | **Integration mode** when `HAMSTACK_INTEGRATION_ID` is set: no token login, no cookie, require the proxy secret, refuse a non-loopback listen address, hide add/delete-rig, take rigs from `HAMSTACK_RIGS` with hamstack's ids, honour `ro` | token cookie with `Path: "/"` (`auth.go:setSession`); `POST/DELETE /api/rigs`; `--dev-no-auth` is the only no-login mode and is a dev flag |
| 3 | `frame-ancestors 'self'` and no `X-Frame-Options: DENY` in integration mode | `server.go:104,112` |
| 4 | **Unkey first on shutdown**, with a context SIGTERM does not cancel; finish within 5 s; unkey at start-up, on shutdown and when the last controlling WebSocket leaves **only if this program keyed** (persisted marker file, cleared on unkey, so a crash while keyed is still recoverable; a blind `set_ptt 0` would cut another program's transmission on the shared rigctld); retry a failed unkey | `main.go`: `srv.Shutdown` (10 s) runs before `mgr.Shutdown`; `Rig.Unkey` ignores the `SetPTT` error |
| 5 | **Versioned integration surface**: `/api/v1/integration` and `/api/health` via `hamgo/integration`; written change policy (additive within v1) in its docs | unversioned API |
| 6 | Do not print the token in integration mode; never log the proxy secret | start-up banner |
| 7 | Keep: release asset names, `sha256sums.txt`, `-version` | already right |

Standalone behaviour does not change in any of these: with no `HAMSTACK_INTEGRATION_ID`, rigctlWeb is exactly what it
is today. Items 1-6 touch `cmd/`, `internal/server` and `web/`; they do not touch `internal/rigctl` or
`internal/audio`, so they can proceed in parallel with the hamgo moves without two agents editing the same files.

### 5.3 hamstack's own radio panel and rigctlWeb, long term

**Recommendation: hamstack keeps a light native radio card built on hamgo, and defers everything richer to
rigctlWeb.** It does not grow into a second rigctlWeb, and it does not disappear.

- Keep, native: per rig status and service buttons, frequency, mode, power, the meters, tuner on/off, the owner-gated
  tune cycle with Stop, and the audio monitor at full parity (section 3.4). This is what `rig.html` and
  `cmd/hamstack/webrig.go` do today plus audio.
- Why keep it: hamstack must be complete with no integration installed; the Overview needs rig state without starting
  another process; tune and service control are tied to hamstack's own switches and units; a Pi with little memory
  may not want a second server.
- Why not grow it: VFO A/B, split, RIT/XIT, memories, per-band memory, filter width, AGC/preamp/attenuator, FM tones,
  views and search exist in rigctlWeb, driven by its poller and capability model. Rebuilding them on hamstack's plain
  pages is exactly the duplicate codebase the owner decided against. The `docs/tasks.md` item "Panel ideas from
  rigctlWeb to consider" is therefore closed as "use the integration".
- Which side yields where the two overlap today:
  - **rigctld client and caps:** hamstack yields; it adopts `hamgo/rigctl` (rigctlWeb's API). Cost: 11 call sites in
    two files (`cmd/hamstack/webrig.go`, `cmd/hamstack/ardopcf.go`) plus the test fake.
  - **Audio:** hamstack yields on protocol and code (section 3); rigctlWeb gains `Pick`.
  - **Tune and TX limits:** neither yields now. Each enforces its own limits on its own code path. If `txguard` is
    extracted later, rigctlWeb's implementation leads, moves with its tests, and hamstack's tune adopts it.
  - **Poller and live state:** rigctlWeb's stays in rigctlWeb. hamstack keeps its simple snapshot polling with the
    slow-read pause (`webrig.go:snapshot`, `paused`). It is not a goal to share them.

## 6. Migration plan

### 6.1 Constraints that shape the order

- A committed `go.mod` cannot require an unpublished module without breaking clean builds. So: hamgo is staged
  locally, the owner publishes and tags, and only then do consumers switch, **deleting their copy in the same commit
  that adds the import**. There is never a commit on a consumer's main with both.
- Two agents editing the same code in two trees is the failure mode. Each component has one source of truth per step:

| Component | Source of truth now | Changes hands when | Then |
|---|---|---|---|
| rigctld client + caps | `rigctlWeb/internal/rigctl` | step 5 (rigctlWeb switches to the tag) | `hamgo/rigctl` |
| hamstack's rigctld client | `hamstack/internal/rigctl` (bug fixes only) | step 6 | deleted |
| Audio capture, mixer | `rigctlWeb/internal/audio` | step 9, "audio move day" | `hamgo/audio` |
| hamstack `internal/audiomon` | **frozen from today**: no features; a fix is made in rigctlWeb first | step 10 | deleted |
| Audio HTTP handlers | `rigctlWeb/internal/server/audio.go` | step 12 | `hamgo/audio/audiohttp` |
| Audio JS | `rigctlWeb/web/src/lib/{audio,autolevel}.ts` + stores | step 13 | `hamgo/audio/web` |
| Plugin manifest | `hamstack/internal/plugin` | step 16 | `hamgo/manifest` |
| safetext | `hamstack/internal/safetext` | step 6 | `hamgo/safetext` |
| gps, agw, usbdev, alsa, hamlib | `hamstack/internal/...` | their own step (18) | hamgo |

- **Staging window.** Between "copied into local hamgo" and "consumer switched", the original is still the truth. The
  hamgo session re-copies before the tag and records the source commit hash in the hamgo commit message. A
  `scripts/check-staged.sh` in hamgo diffs staged packages against the source trees and is run before every tag. The
  window is kept short: one component at a time.
- **Audio timing (decision).** Do not wait for the waterfall, `audio setup|revert` or macOS/Windows testing: none of
  them exists yet, and waiting postpones hamstack's parity indefinitely. Instead audio moves on a named **move day**
  with these conditions: the rigctlWeb session has no uncommitted or in-progress audio work; it commits or shelves
  first and says so in `rigctlWeb/docs/ROADMAP.md`; from that commit on, `rigctlWeb/internal/audio` is read-only
  until step 10 deletes it. New audio features are then written in hamgo, by one session at a time, recorded as
  "audio: held by <session>" in hamgo's `docs/OWNERS.md`.

### 6.2 Steps

Each step is one session's work, leaves every repo building and tested, and can ship alone. "Gate" means stop and get
the owner's explicit yes.

| # | Repo | Step | Done when |
|---|---|---|---|
| 0 | all | Paste the "what every agent must know" box into `hamstack/docs/tasks.md`, `rigctlWeb/docs/ROADMAP.md`, `voice/CLAUDE.md`. Record the audiomon freeze | docs only |
| 1 | hamstack | **Gate.** Module path: one mechanical commit to `github.com/ATCUSA/hamstack` (2.6). Then, separately, the owner decides how the branch meets `origin/main` (merge or rebase; 10 commits behind incl. dependency bumps and `site/`) | `go build ./... && go test ./...` pass; no other diff |
| 2 | hamgo | Create `~/Projects/hamgo` locally (`git init`, no remote): `go.mod`, LICENSE (MIT), README with the free-forever statement, CI file, the dependency-rule test, `~/Projects/go.work` (uncommitted, outside the repos) | `go test ./...` passes on an empty library |
| 3 | hamgo | Stage `safetext` (from hamstack) and `rigctl` + `rigctl/rigctltest` (from rigctlWeb, with `testdata/*.caps` and its tests; then port `Abort`, the power-range helpers and hamstack's test cases) | hamgo tests pass; `check-staged.sh` clean |
| 4 | hamgo | **Gate.** Owner creates `ATCUSA/hamgo` (public), pushes, tags `v0.1.0`; one online `go mod download` on the dev machine | tag resolves through the public proxy |
| 5 | rigctlWeb | Branch: `require hamgo v0.1.0`; replace `internal/rigctl` imports; delete `internal/rigctl`; the test that writes `web/src/lib/testdata/*.json` for the UI tests (`caps_test.go`, near line 198) stays in rigctlWeb as a small test reading the `.caps` fixtures from the hamgo module folder. No behaviour change. **Contract that moves with the client, with its tests:** the retry policy (a command is never retried after a failure if it is `vfo_op`, `send_*`, `scan`, `reset`, `power*`, `stop_*` or `wait_*`; only safe reads and idempotent sets are repeated), because a repeated transmit-adjacent command is a safety fault. When hamstack's `Abort` and the power helpers are merged in (step 5/10) they are listed under the same contract | `go test ./...`, `pnpm test`, `GOWORK=off go build` on a clean cache |
| 6 | hamstack | Switch `cmd/hamstack/webrig.go`, `ardopcf.go` and tests to `hamgo/rigctl` (contexts with the existing 3 s timeout; keep the caps cache in `rigLink`); switch `safetext` imports; delete `internal/rigctl`, `internal/safetext` | tests pass; `hamstack run unkey` and tune tests unchanged in meaning |
| 7 | voice | Replace `rigctld.go` with `hamgo/rigctl` (one connection per command kept); safety tests unchanged. Note: the voice repo has no commits yet (`git log` on 2026-11-15), so its first commit is the owner's call | `go test -race ./...`; manifest still validates |
| 8 | rigctlWeb | 5.2 item 4 (unkey-first shutdown within 5 s, start-up unkey, unkey when the last controller leaves, retry; **all unkeys only if rigctlWeb keyed, via the marker file**). Good for standalone use too; independent of hamgo; any time after step 0. Items 3 and 6 need the integration-mode switch and go with step 15 | new tests: SIGTERM unkeys before listeners close, within 5 s |
| 9 | hamgo | **Audio move day.** Move `rigctlWeb/internal/audio` as is; add `Pick` and hamstack's two detection tests; export `InputMixer`. Tag `v0.2.0` (**gate**: tag) | hamgo tests pass |
| 10 | rigctlWeb, hamstack | Same day, one after the other: rigctlWeb imports `hamgo/audio`, deletes `internal/audio` (its `server/audio.go` stays, importing hamgo). hamstack imports `hamgo/audio` in `cmd/hamstack/listen.go` and `internal/web/audio.go`, deletes `internal/audiomon`. No feature change in either | both build with `GOWORK=off`; hamstack's Listen still works as before |
| 11 | hamgo | Write `docs/audio-protocol.md` (3.2) from rigctlWeb's behaviour; add protocol tests against a fake `Store`/`Rigs` | docs + tests |
| 12 | hamgo, rigctlWeb | `audio/audiohttp` (Settings, Input, Stream; no embed yet): generalise `rigctlWeb/internal/server/audio.go` behind `Options`; move `TestAudioEndpointsAreGuarded`-style logic tests (auth part stays in rigctlWeb) and `TestAudioInputVolumeRemembersAndRestoresOriginal`. Tag `v0.3.0` (**gate**). rigctlWeb mounts it with adapters over `config.Store` and `rig.Manager`; deletes `server/audio.go`. Routes and JSON unchanged | rigctlWeb's UI works untouched; server tests pass |
| 13 | hamgo, rigctlWeb | `audio/web/hamgo-audio.js` + `.d.ts` + node tests, embedded and served by `Handlers.Script`. Tag `v0.4.0` (**gate**). rigctlWeb vendors it, stores become wrappers, drift test added, moved vitest tests deleted | `pnpm build` offline; `AudioPanel.svelte` unchanged; drift test passes |
| 14 | hamstack | Audio parity: `Store` over hamstack's config; `Rigs` from rig config + `alsa` card name + running modem names for `SharedBy`; mount handlers; static route; radio card uses the module (settings, input slider, auto-adjust, restore); ledger entry for the input volume via `OnInputChange` and restore on uninstall; confirm dialog when `sharedBy` is not empty | browser check on the dev machine with a fake capture; then **gate**: owner verifies on the Pi |
| 15 | rigctlWeb | Integration mode 5.2 items 1, 2, 3, 5, 6 (base path, proxy secret, `HAMSTACK_RIGS`, frame headers, `/api/v1/integration`, banner) using `hamgo/integration` (added to hamgo in the same wave, tag **gate**) | test: every URL in served HTML is under the base path; requests without the secret get 401 |
| 16 | hamgo, hamstack | `manifest` moves (schema 1 unchanged, schema 2 `kind` added); `hamstack plugin validate` calls it; apply the `docs/plugins/` edits of 4.7 | validator tests pass in hamgo; examples validate |
| 17 | hamstack | Reverse proxy + integration runtime + `/api/v1` (5.1). First with the world clock plugin (no radio), then rigctlWeb Managed mode read-only, then the transmit switch | each sub-step demonstrable locally; **gate** before any Pi install |
| 18 | hamgo, hamstack | Second wave, one package per step: `gps`, `agw`, `usbdev`, `alsa`, `hamlib` | each: move, switch imports, delete, test |

Steps 5-7 are independent of each other once step 4 is done. Step 8 is independent of everything. Steps 9-14 are
strictly ordered. Step 17 needs 15 and 16.

### 6.3 Stop and ask the owner

- Creating `ATCUSA/hamgo` (or any repository) on GitHub; making anything public.
- Any `git push`, in any repo. Any tag. Any merge to `main`.
- Changing hamstack's module path (step 1) and how the local branch is reconciled with `origin/main`.
- The first `go mod download` of a hamgo tag (needs the network) and any change to `GOPROXY`/`GOFLAGS`.
- Anything on the Pi: deploying, SSH, verifying audio. Agents do not connect to it.
- Anything that could transmit: starting ardopcf or a modem session, a tune cycle, keying through a fake that is not
  provably fake.
- Changing a safety limit (TX timeout, tune cooldown, carrier cap, TOT) or extracting `txguard`.
- Changing a system sound setting on a real machine during development (the Mixer is tested with fakes only).
- Deleting or rewriting `docs/modules.md`, and applying the `docs/plugins/` edits.
- Naming or scoping small-login; deciding voice versus rigctlWeb mic transmit.

## 7. Risks and open questions

**Risks**

- **Audio on the Pi (owner's findings).** PipeWire holds the radio's USB codec, so plain ALSA capture fails with
  "busy"; `pw-record` works. The Pi has no `pactl`, so devices come from `ffmpeg -sources pulse` (both covered by
  fixtures that must move with the code). The codec carries audio on the **left channel only**, so `mix` halves the
  level (`toMono` averages) and the right channel is silence: hamstack's default channel for a rig should be settable
  from its profile, and the auto-level works on what is heard, so it stays correct.
- **The monitor must never stop a modem or an app.** `hamgo/audio` only starts and stops its own capture process.
  The contract forbids a host from wiring the monitor to service control; `Rig.SharedBy` is display only. The plugin
  spec's "hamstack stops that rig's HF modem" applies to `needs.audio` plugins (exclusive use), not to the monitor.
- **ardopcf uses ALSA directly.** While PipeWire holds the codec, a modem opening `plughw:` directly may fail or may
  take the card away from PipeWire; a wrapper (`pw-jack`, `pipewire-alsa`, or stopping PipeWire's use of that card)
  changes who owns the device. Unverified on the Pi (`docs/tasks.md`). Until it is, the monitor must tolerate losing
  the device (it does: the capture ends and the listener is told why) and hamstack must not "fix" contention by
  touching the modem.
- **Input volume is a system-wide setting.** `Mixer.Set` changes the level every program on that input hears,
  including a running modem's decode level. Mitigations in the design: explicit action only, 5-100 % clamp, original
  remembered and restorable, hamstack ledger entry, confirm when `sharedBy` is not empty. Whether a wpctl/pactl
  volume change affects a program on raw ALSA is **unverified**; treat it as affecting it.
- **Licensing.** Everything is MIT by one owner, so moving code between repos needs no relicensing; hamgo's LICENSE
  carries the same copyright line. The local hamstack branch has no `LICENSE` file (`origin/main` does): restore it at
  step 1. `coder/websocket` is ISC/MIT-compatible; record it in hamgo's README.
- **A v0 API break lands in three programs.** Mitigated by tags, the CHANGELOG rule and by switching consumers one at
  a time.
- **Doubled polling.** hamstack's card and rigctlWeb both poll rigctld. Keep hamstack's snapshot at its current gentle
  rate; revisit only if a slow CAT link shows it.
- **Proxy work is new.** hamstack has no HTTP reverse proxy; base-path bugs in proxied apps are the likeliest source
  of trouble. The world clock goes first for that reason.
- **Loopback trust.** The proxy secret stops casual local access to the app; rigctld remains open to local processes.
- **Proxied pages have the dashboard's authority.** Anything served under `/plugin/<id>/` or `/integration/<id>/` is
  same-origin with the dashboard, so its script can do whatever the logged-in user can (start services, tune if
  enabled, run jobs). Installing a plugin is therefore trusting its code like hamstack's own; checksums prove origin,
  not intent. The install dialog must say so. Real isolation needs a separate origin (another port or host name) or
  a sandboxed frame without `allow-same-origin`, which breaks cookies-free same-origin fetches; not designed here.

**Open questions for the owner**

1. Go-ahead to create and publish `ATCUSA/hamgo`, and the tag cadence (the plan needs a tag at steps 4, 9, 12, 13, 15, 16 and one per
   second-wave package: about ten in all, each an owner gate unless the owner delegates tagging).
2. hamstack's branch: merge or rebase onto `origin/main`, and when (it changes 100 files' imports).
3. hamstack's audio default: Listen available on every rig with a sound card (as today), or opt-in per rig like
   rigctlWeb?
4. Should hamstack's dashboard offer the input-volume controls at all on a rig whose modem is running, or only show
   them with the warning (the design's choice)?
5. small-login: what must it be compatible with ("eight-if")? That decides native component versus integration.
6. Voice: remains a hamstack plugin, or becomes rigctlWeb's planned "transmit audio from the browser mic" and reaches
   hamstack through the integration? Building both duplicates a transmit path.
7. Does any integration need to call hamstack from its server (not its page)? If yes, a token-file loopback API is
   added to `/api/v1`; if no, it stays out.
8. URL prefix `/integration/<id>/`: acceptable, or a shorter name?
9. Is a Unix socket (file permissions instead of a proxy secret) preferred for hamstack-to-app traffic? It is
   stronger and needs no secret, but needs a `-listen unix:` option in each app and differs from the plugin contract.
10. Who verifies macOS and Windows audio before those paths lose their "untested" note?
