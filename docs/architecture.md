# How hamstack fits together

This is the map for people who want to change it. For using it, start at the [README](../README.md).

## The idea

hamstack does not run radio software itself. It **plans**, **shows**, and **delegates**:

1. It keeps your choices in one small config file (`~/.config/hamstack/config.json`): rigs, stations, sites, GPS, AX.25.
2. From that it generates configuration for each program (`mercury.ini`, `direwolf.conf`, Pat's config) and **systemd user units**.
3. The units do not contain device paths or ports. Each runs `hamstack run <kind> <rig>`, which reads the config and works
   out the serial port and sound card *at the moment the service starts* (by USB identity), then `exec`s the real program.
   Replugging a radio, using another USB port, or moving to another site therefore needs no new units.
4. Anything that changes the system (packages, downloads, builds) is built as a list of `install.Step`s, shown to the user,
   confirmed, and only then run.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/hamstack` | The CLI. One file per area. Thin: it parses flags, calls `internal/*`, prints. `tuiback.go` adapts the same operations for the TUI. |
| `internal/config` | The config model and its rules: rigs (each with its own ports), stations, sites, GPS, AX.25, per-rig Direwolf options. Atomic writes; migrations from older layouts. |
| `internal/catalog` | The program catalog (name, binary, version flag, upstream, distribution packages, build recipe, requires/suggests/conflicts) and the dependency logic (`Closure`, `Suggestions`, `Conflicts`). |
| `internal/install` | Install plans: distribution packages (apt/dnf/pacman), upstream release downloads with checksums, generic source builds (autotools, cmake, make), staged installs that can never write outside `~/.local`. `install.Run` shows and confirms. |
| `internal/versions` | Installed version (from the binary, a package database, or a record hamstack keeps for its own builds) against the newest upstream release (GitHub, SourceForge). |
| `internal/units` | Generates the systemd user units and the per-rig modem configs. |
| `internal/profile` | Radios and interfaces as JSON, embedded in the binary, plus the user's own in `~/.config/hamstack/profiles`. |
| `internal/doctor` | The checks. Each returns a result with a fix; system access is injected so tests can fake it. |
| `internal/tui` | The Bubble Tea interface: model, keys, view and prompts. It talks to a `Backend` interface, so it is tested with a fake. |
| `internal/agw` | A small AGWPE client (Direwolf's TCP interface) for `hamstack packet listen|call`. |
| `internal/gps`, `internal/ax25`, `internal/netinfo`, `internal/hamlib`, `internal/usbdev`, `internal/audio`, `internal/groups`, `internal/patcfg`, `internal/svc`, `internal/distro`, `internal/safetext` | Focused helpers; each reads `/proc`, `/sys` or a command's output, with the root or command passed in for tests. |

## Ports

Each rig gets its own so several can run at once: rigctld from 4532; Mercury's block of ten from 8300 (ARQ control, data,
broadcast and web UI derived from it); Direwolf's AGW at 8000+2n and KISS at 8001+2n; hamstack's gpsd on 2948 (not gpsd's
standard 2947, so a system gpsd cannot clash).

## Exit status 3

A hamstack service that exits with status 3 means "the hardware is not there" (radio unplugged, GPS missing). The units list
it in `RestartPreventExitStatus`, so an unplugged radio does not cause a restart loop, and `hamstack status` shows **no radio**
instead of failed.

## Testing layers

1. **Unit tests** (`task test`): pure logic, fakes for the system. Seconds.
2. **Incus scenarios** (`task incus-test`): the real binary under real systemd in Debian, Fedora and Arch containers as an
   unprivileged user with sudo, the way a user runs it. About 190 assertions per distribution.
3. **A VM** for the kernel AX.25 stack (containers share the host kernel, which may not have the module).
4. **Real hardware**: by contributors. See [hardware-testing.md](hardware-testing.md). This is the layer with the fewest tests.
