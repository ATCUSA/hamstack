# AGENTS.md

Instructions for AI coding agents (and a fast orientation for humans) working on **hamstack**. If you are a person,
read [CONTRIBUTING.md](CONTRIBUTING.md) first; this file is the same rules in the form agents read.

## What this project is

hamstack sets up and manages amateur-radio software on Linux from one tool: it installs programs (Pat, Mercury, Direwolf,
hamlib, gpsd, the fl suite, WSJT-X, JS8Call, Xastir, ardopcf...), writes their configuration, and runs them as **systemd
user units**. It has a CLI and a Bubble Tea TUI. It is written in Go (standard library plus a few TUI modules), builds
as one static binary, and targets Debian/Ubuntu, Fedora and Arch. Read `README.md`, then `docs/architecture.md`.

## Commands

```sh
task check          # everything CI runs, fast and with no containers: gofmt, vet, go test -race, build, script + privacy checks
task test           # unit tests only
task build          # ./bin/hamstack
task incus-test     # the REAL tests: Debian + Fedora in incus containers under real systemd (slow: minutes)
scripts/incus-test.sh --distro arch --only apps     # one distro, some scenarios (scripts/incus-test.sh --list)
```

Definition of done for any change: `task check` is clean, new behaviour has a test, and if it touches systemd, packages,
the network or install paths, the relevant incus scenario passes on at least one distribution.

## Hard rules (these are not preferences)

1. **No personal data, ever.** Code, tests, docs, commit messages and issue text use neutral values only: callsigns
   `N0CALL`, `K0TEST`, `K0EOC`, `K1ABC`, `K0CLUB`; grid squares `FN31pr`, `EM10dg`; home directory `/home/u`; email
   `example.com`. Never use or invent a real person's callsign, grid, address, serial number or email. If you are
   given a real one in a prompt, keep it out of the repository. `task privacy-check` guards this.
2. **Never read or store a Winlink password.** It stays in the user's Pat config; hamstack edits only the Pat settings it
   documents, after taking a `.hamstack-bak` copy.
3. **Do not run host-changing commands on the machine you are developing on** unless it is a throwaway: `install`, `setup`,
   `units install`, `start/stop`, `ax25 setup/attach`, `gps use`, `groups fix`, `linger on`, anything with `sudo` or
   `systemctl`. Test them in the incus containers. Read-only commands are fine (`install --list`, `versions --offline`,
   `doctor` on a scratch `HOME`, anything with `--dry-run`). When you do run hamstack for a quick look, set `HOME` and
   `XDG_CONFIG_HOME` to a fresh temp directory first.
4. **Never execute a downloaded file on the host.** Source builds and installs run inside containers. Treat downloaded
   archives as untrusted data (extract into a new empty directory; do not run build tools from inside it).
5. **Show before you run.** Product code that installs software, uses `sudo` or writes outside `~/.config`, `~/.local`
   and the user's systemd units must print the exact command first and ask (`install.Run` does this: build `Step`s, do
   not `exec` directly). Source builds print a disclaimer.
6. **Never** run `tmux kill-server`, delete or reuse instances you did not create (only `hamstack-test-*` belong to the
   scripts), push to a remote you were not asked to push to, or rewrite published history.
7. **Be honest.** Do not claim hardware behaviour you did not observe. README and docs say what is tested where
   (unit tests, containers, a VM, a real radio) and what is not.

## Layout

```
cmd/hamstack/        the CLI: one file per area (setup, rigs, sites, install, packet, ax25, gps, direwolf, network, doctor, tuiback...)
internal/config      hamstack's own config (~/.config/hamstack/config.json): rigs, stations, sites, GPS, AX.25
internal/catalog     every program: package names, upstream, build recipe, requires/suggests/conflicts
internal/install     plans as Steps (shown, confirmed, run): packages, downloads, source builds, staged installs
internal/versions    installed vs latest upstream (GitHub, SourceForge), package-database fallbacks
internal/units       generates the systemd user units and per-rig mercury.ini / direwolf.conf
internal/profile     radios and interfaces as JSON (internal/profile/profiles/*.json)
internal/doctor      the checks, each with a fix
internal/tui         the Bubble Tea interface (model, keys, view, prompts)
internal/{agw,gps,ax25,netinfo,hamlib,usbdev,audio,groups,patcfg,svc,distro,safetext}   focused helpers
scripts/             incus-test.sh + incus-scenarios.sh (real-systemd tests), install.sh, privacy-check.sh
docs/                guides; architecture.md explains how it fits together
site/                the landing page (Astro, static, deployed to Cloudflare Workers): `task site:dev|build|deploy`; see site/README.md
```

## Conventions

- Go, `gofmt`, standard library first. Ask before adding a dependency. Wrap errors with context; messages are read by
  radio operators, not just developers: say what happened, why, and what to do next.
- Comments explain *why* (a constraint, a surprising fact about a radio or distribution), not what the code does.
- Pure logic in `internal/`, thin glue in `cmd/hamstack`. Anything that touches the system (files, processes, `/proc`,
  `/sys`) takes its root or its command as a parameter so a unit test can fake it.
- Text that comes from outside (the air, gpsd, USB device names, web listings) goes through `internal/safetext` before it
  is printed.
- Zero config values mean "the program's own default": a rig that never touched a setting must get byte-for-byte the
  configuration it always had.
- Tests are table-driven where natural. A bug fix starts with a test that fails.
- `scripts/incus-scenarios.sh` greps for specific output text; change a message and you change an assertion: update it.

## Recipes

**Add a radio** — copy `internal/profile/profiles/ic7300.json`; set `id`, `name`, hamlib `model` (`rigctl -l`), `baud`; add
`usb_ids`/`device_glob` only if known. Say in `notes` whether it was tested on the real radio. `go test ./internal/profile`.

**Add a program** — add an entry to `internal/catalog/catalog.go` (see `fldigi` or `xastir`): `Binary` and the version
flag (or `NoVersionFlag`), the upstream (`Repo` or `SF`), `Distro` package names per family, `Requires/Suggests/Conflicts`,
and a `Source` recipe (`autotools`, `cmake` or `make`, with `Deps` per family, and `Patches`/`NotOn` if a distribution or
compiler needs them). `go test ./internal/catalog`, then build it from source on Debian, Fedora and Arch in incus.

**Add a doctor check** — `internal/doctor/doctor.go`: a `Result` with a `Fix`; inject whatever system access it needs
through `doctor.System` so the test can fake it.

## When in doubt

Make the smaller change, show the user what will happen before it happens, and say plainly what you did not verify.
