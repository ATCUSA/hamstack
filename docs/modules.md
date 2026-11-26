# Sharing code between hamstack, rigctlWeb, the plugins and the community

Status: SUPERSEDED by `docs/architecture/hamgo-and-integrations.md` (plan of record; it also corrects this file: hamstack's module path is already published as github.com/ATCUSA/hamstack). Original status: recommendation, nothing restructured or published yet (publishing needs the owner's go-ahead).

## What is worth making a package

Counted, not guessed: a piece is a candidate when the same code already lives in two or more of our programs and does
not depend on hamstack's own setup (installer, ledger, units, config).

| Candidate | Where it is now | Verdict |
|---|---|---|
| **rigctld client** (talk to Hamlib's `rigctld` over TCP: frequency, mode, levels, PTT, capabilities, error codes, reconnect) | three separate copies: `hamstack/internal/rigctl` (~420 lines), `rigctlWeb/internal/rigctl` (~900), `voice` plugin `rigctld.go` (~43) | **First.** Most duplicated, useful to anyone writing a Go ham tool, no dependencies. Needs one agreed API (rigctlWeb's has context support and is the most complete). |
| **Receive-audio monitor** (`audiomon`: detect PipeWire/Pulse/ALSA, capture command, per-radio Hub that runs capture only while listened to, mono/stereo mix) | `rigctlWeb/internal/audio`, now copied into `hamstack/internal/audiomon`; the voice plugin has its own `arecord` code | **Second.** Already written with no imports from its host; two copies exist as of this commit, so it should become one. Browser side (PCM over WebSocket, AudioBuffer playback, meter) could ship as a small JS file with it. |
| **Plugin manifest** (`internal/plugin`: Parse, Validate, JSON schema) | hamstack only | **Third.** Plugin authors need the validator without installing hamstack; it already has a `hamstack plugin validate` command. A package lets editors, CI and other tools reuse it. |
| Sound card naming and USB radio detection (`internal/audio`, `usbdev`) | hamstack only | Maybe later: Linux specific and tied to our profiles. |
| Installer, ledger, units, doctor, web dashboard, catalog | hamstack only | **Not** candidates: they are hamstack. Keep them internal. |

Rule of thumb: extract on the second or third copy, not before; never extract the parts that only make sense inside hamstack.

## How it works in Go (the mechanics)

- **`internal/` cannot be imported from outside its module.** Anything meant to be shared must live outside `internal/`, or in its own module.
- **A module path is what other people import.** hamstack's module is currently just `hamstack`, which cannot be fetched. It needs a real path (`github.com/ATCUSA/hamstack`) before anything in it can be imported by someone else, which is a rename of the path in `go.mod` and every import. rigctlWeb already uses `github.com/open-rms/rigctlweb`.
- **Versioning is by git tag** (`v0.1.0`; Go treats `v0.x` as "may still change"). Importers pin a tag.
- **Two layouts:**
  1. *One small library repo* (recommended), for example `github.com/ATCUSA/hamgo`, with packages `rigctl/`, `audiomon/`, `manifest/`. One tag, one place to fix bugs, zero third-party dependencies, MIT licence like the rest.
  2. *Separate repo per package.* More tags to manage, more overhead; only worth it if one package grows its own community.
  hamstack, rigctlWeb and the plugins then each `go get` that module. A package inside hamstack's own repo but outside `internal/` also works, but ties the library's versions to hamstack's.
- **Working on both at once before anything is published:** a `go.work` file (or a `replace` line in `go.mod`) points a program at the local copy of the library, so no publishing is needed while it settles.
- **Keep the library honest:** no imports from the host program, no global state, the pieces that run commands take a `has(tool)` function and a runner so tests can use fakes (as `audiomon` does), and tests use output captured from real systems (the ffmpeg 7 "(none)" suffix once hid every device from a parser tested only with an invented format).

## Suggested order

1. Now: keep the boundaries clean (no hamstack imports in `internal/audiomon`; same for a future `rigctl` package). Done for `audiomon`.
2. When the owner approves publishing: pick the module name, rename hamstack's module path, create the library repo, move `rigctl` first (agree rigctlWeb's API as the base), then `audiomon`, then the manifest package; switch hamstack, rigctlWeb and the voice plugin to import them; tag `v0.1.0`.
3. Document each package with a short README and a runnable example, so it is usable without reading hamstack.

## Open questions for the owner

- Library name and whether it lives under `ATCUSA`.
- Is the rigctlWeb client API the one to keep, with hamstack adapting to it (it has contexts and richer errors), or should the smaller hamstack one lead?
- OK to rename hamstack's module path (a one-time mechanical change) when the push is approved?
