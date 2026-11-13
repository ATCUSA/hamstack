# hamstack

[![CI](https://github.com/ATCUSA/hamstack/actions/workflows/ci.yml/badge.svg)](https://github.com/ATCUSA/hamstack/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Set up and run amateur radio software on Linux without the yak-shaving.**
hamstack installs the programs, writes their configuration, finds your radio's USB devices, and runs everything as
systemd user services you can start, stop and watch from one command or one terminal screen.
Winlink over HF or VHF packet, digital modes, APRS, GPS: pick what you want and hamstack wires it together.

It is one static binary. It shows you every command before it runs it, never touches your Winlink password, and keeps
its own state in `~/.config/hamstack` so it is easy to see and to undo.

```text
$ hamstack status
rigs: hf, pkt   station: club (N0CLUB, web UI localhost:8080)

SERVICE        STATE                     AT LOGIN  SINCE
rigctld@hf     active (running)          enabled   Fri 2026-11-13 18:04:02 UTC
mercury@hf     active (running)          enabled   Fri 2026-11-13 18:04:07 UTC
direwolf@pkt   no radio (plug it in, then: hamstack start direwolf@pkt)   enabled   -
pat            active (running)          enabled   Fri 2026-11-13 18:04:07 UTC
```

> **Status: early.** hamstack is exercised thoroughly on Debian 13, Fedora 45 and Arch in containers (about 190 checks per
> distribution under real systemd), and its kernel AX.25 support on a real kernel in a VM. **Very little has been tried with
> real radios, sound interfaces or GPS receivers yet**, and the profiles for most radios are written from hamlib's own list and
> typical settings, not tested. If you can try it on real hardware, [please tell us how it went](docs/hardware-testing.md).

## Contents

[Install](#install) · [First run](#first-run) · [What you can do](#what-you-can-do) · [Programs](#programs) ·
[Everyday commands](#everyday-commands) · [How it works](#how-it-works) · [Safety and privacy](#safety-and-privacy) ·
[What is tested](#what-is-tested) · [Documentation](#documentation) · [Contributing](#contributing)

## Install

You need Linux with a **systemd user session** (any normal desktop login, or a Raspberry Pi running Raspberry Pi OS),
and Debian/Ubuntu, Fedora or Arch (or a derivative). Packages are installed with your own package manager and `sudo`,
after showing you the command.

**From a release** (a static binary into `~/.local/bin`, checksum-verified; no root):

```sh
curl -fsSL https://raw.githubusercontent.com/ATCUSA/hamstack/main/scripts/install.sh | sh
```

Read the script first if you like: it is short, it only writes `~/.local/bin/hamstack`, and it verifies the
download against the release's `SHA256SUMS`. `HAMSTACK_VERSION=v0.1.0` pins a version.

**From source** (Go 1.27 or newer):

```sh
git clone https://github.com/ATCUSA/hamstack && cd hamstack
go build -o ~/.local/bin/hamstack ./cmd/hamstack      # nothing else needed
# or, with Task (https://taskfile.dev):  task install
# or:  go install github.com/ATCUSA/hamstack/cmd/hamstack@latest
```

Make sure `~/.local/bin` is on your `PATH`.

## First run

Run `hamstack` in a terminal. On a fresh install it offers the **guided setup**; it asks a few questions (what you want to
set up, which radio, your callsign and grid square), installs what is missing, writes the configuration and the services,
runs a health check, and starts everything. You can decline and run it later with `hamstack setup winlink`.

**No radio yet?** Choose the `dummy` radio to try everything without hardware.

The first screen is the radio list: the dummy rig, a Digirig, eight common rigs and FLRig, and **`s` to search all 300+
radios** that hamlib supports. A radio that is not plugged in is fine: its services wait and show *no radio* until it is.

After setup, `hamstack` opens the interface (a tab each for services, rigs, sites, stations, devices, programs, settings,
doctor, logs and network), or use the commands below.

## What you can do

- **Winlink**: Pat with an HF modem (Mercury) or VHF/UHF packet (Direwolf), for one or several callsigns.
- **Several radios at once**: each rig gets its own ports and services; HF on one, packet on another.
- **Sites**: save "home", "the EOC" and "the club" as named setups and switch with one command; `hamstack site here`
  finds the one whose radios are plugged in.
- **Radios found by USB identity**, not by `ttyUSB0`: replugging or using another port just works; `hamstack devices` groups
  a radio's serial port and sound card.
- **Programs beyond Winlink**: the fl suite, WSJT-X Improved, JS8Call, Xastir and more, each installable on its own, each
  version-checked against upstream and buildable from source when your distribution's package is old.
- **GPS**: a gpsd service, and your grid square read from a fix.
- **Packet tools with no kernel support or root**: `hamstack packet listen` and `packet call` talk to Direwolf directly.
- **The Linux AX.25 stack** (axports, kissattach, axcall, axlisten) set up for you, where your kernel still has it.
- **Direwolf configured for you**: modem speed, callsign and SSID, key-up timing, digipeating, and your own extra lines.
- **A network view**: every port the stack uses and who on your network can reach it.
- **`hamstack doctor`**: checks that everything is ready and says how to fix what is not.

## Programs

Each is installed, version-checked and updated the same way; requirements are added with the reason, suggestions are only
offered, and programs that conflict refuse to install together.

| Program | What it is |
|---|---|
| **Pat** | Winlink client (telnet, packet, HF through a modem) |
| **Mercury** | open HF modem, VARA-compatible interface (the usual partner for Pat on HF) |
| **ardopcf** | ARDOP HF modem that Pat can also use |
| **Direwolf** | soundcard packet TNC for VHF/UHF (AX.25, APRS) |
| **hamlib** | rig control (`rigctld`) |
| **FLRig, fldigi, flmsg, flamp** | the fl suite: rig control, digital modes, forms, file transfer |
| **WSJT-X Improved / WSJT-X** | FT8, FT4, WSPR and friends |
| **JS8Call** | keyboard-to-keyboard messaging on HF |
| **Xastir** | APRS client with maps |
| **gpsd, ax25-tools** | GPS daemon; the kernel AX.25 tools |

`hamstack install` with no names shows the full list to choose from; `hamstack install --list` prints it as a table with how each
would be installed on your machine. Details: [docs/programs.md](docs/programs.md).

## Everyday commands

```sh
hamstack                               # the interface (also: hamstack tui)
hamstack setup winlink                 # guided setup, any time
hamstack status | start | stop | restart [SERVICE|all]
hamstack logs SERVICE [-f]
hamstack doctor                        # checks, each with a fix
hamstack devices [watch]               # USB serial ports and sound cards, grouped; watch shows plug and unplug live

hamstack rigs | rig add|set|remove|enable|disable|identify
hamstack sites | site add|use|here
hamstack stations | station add|import-pat|remove | use STATION

hamstack install [PROGRAM...] [--from-source] [--list] [--dry-run]
hamstack versions | update             # installed against newest upstream

hamstack direwolf setup|set|show|edit  # configure a rig's Direwolf
hamstack packet listen | call CALL     # watch the air / make a call, through Direwolf (no root, no kernel AX.25)
hamstack ax25 setup|attach|call|listen # the Linux kernel AX.25 stack, where available
hamstack gps use | gps | gps grid --save
hamstack network                       # ports in use and who can reach them
hamstack help                          # everything
```

## How it works

hamstack does not run radio software itself; it plans, shows and delegates. Your choices live in one small config file; from it
hamstack generates each program's configuration and a set of **systemd user units**. The units hold no device paths: each runs
`hamstack run <kind> <rig>`, which finds the radio's serial port and sound card *when the service starts*, by USB identity, and
then hands over to the real program. See [docs/architecture.md](docs/architecture.md).

## Safety and privacy

- **It shows you what it will do first.** Installing packages, `sudo` steps, source builds and file changes outside
  `~/.config`, `~/.local` and your user systemd units are listed and confirmed before they run (`--dry-run` shows the plan only).
- **Your Winlink password is never read or stored by hamstack.** It stays in Pat's own config; hamstack edits only the few Pat
  settings it documents (locator, rig connections, modem address), after saving a `.hamstack-bak` copy.
- **Some programs open network ports to your whole network with no password**: Mercury's web UI and Direwolf's AGW and KISS ports.
  `hamstack network` and `hamstack doctor` flag them. Use a firewall on a network you do not trust.
- **Source builds run upstream code** that hamstack downloads over HTTPS; for several projects no checksum is published to
  check against. hamstack says so before it starts. It never runs AUR helpers or builds AUR packages.
- **Text from the air, gpsd and USB devices is cleaned** before it reaches your terminal.
- **Transmitting is your responsibility.** hamstack never transmits by itself. You are responsible for operating within your licence.

Security problems: please report them privately ([SECURITY.md](SECURITY.md)).

## What is tested

| Layer | What | Where |
|---|---|---|
| Unit tests | the logic, with the system faked | every change (`task check`) |
| Container scenarios | the real binary under real systemd, installing, setting up, switching sites, rebooting, building programs from source, the interface driven through tmux: about 190 checks | Debian 13, Fedora 45, Arch |
| VM | the Linux kernel AX.25 stack with `kissattach`, `axcall`, `axlisten` | a Debian 13 kernel |
| Source builds | fl suite, Xastir, JS8Call, ardopcf, WSJT-X Improved built from upstream source and installed | Debian, Fedora, Arch (the official WSJT-X on Debian and Fedora; Arch no longer ships a Qt 5 module it needs) |
| **Real hardware** | a radio, a Digirig, a GPS receiver, on the air | **not yet: this is where help is most wanted** |

The Qt programs were checked to install and report a version, not launched (they need a display). Packet `call` and `listen` are
tested against a simulated AGW server and a real Direwolf without a radio. Details: [docs/testing.md](docs/testing.md).

## Documentation

| | |
|---|---|
| [The interface](docs/interface.md) | the tabs and keys |
| [Radios, sites and stations](docs/radios-and-sites.md) | rigs, choosing a radio, sites, callsigns |
| [Hardware, permissions and services](docs/hardware-and-services.md) | finding devices, groups, what runs and why |
| [Programs and updates](docs/programs.md) | the catalog, dependencies, source builds, keeping current |
| [Packet radio](docs/packet.md) | Direwolf, `packet listen|call`, the kernel AX.25 stack |
| [GPS](docs/gps.md) | gpsd and your grid square |
| [Network](docs/network.md) | the ports and who can reach them |
| [Testing](docs/testing.md) · [Testing with real hardware](docs/hardware-testing.md) | how it is tested, and how you can help |
| [How it fits together](docs/architecture.md) | for people changing it |

## Contributing

Bug reports, hardware reports, radio profiles, new programs, docs fixes and code are all welcome: see
[CONTRIBUTING.md](CONTRIBUTING.md). The most valuable thing you can do is **try it with real hardware** and file a
[hardware report](https://github.com/ATCUSA/hamstack/issues/new?template=hardware_report.yml).
AI coding agents are welcome under the same rules as anyone: [AGENTS.md](AGENTS.md) has them in the form agents read.
Please follow the [code of conduct](CODE_OF_CONDUCT.md).

## Licence and credits

[MIT](LICENSE). hamstack installs and configures other people's work and is not affiliated with any of it: thanks to the authors
of [Pat](https://github.com/la5nta/pat), [Mercury](https://github.com/Rhizomatica/mercury), [Direwolf](https://github.com/wb2osz/direwolf),
[hamlib](https://github.com/Hamlib/Hamlib), [ardopcf](https://github.com/pflarue/ardop), [gpsd](https://gpsd.io), the
[fl suite](https://sourceforge.net/projects/fldigi/), [WSJT-X](https://wsjt.sourceforge.io/) and
[WSJT-X Improved](https://sourceforge.net/projects/wsjt-x-improved/), [JS8Call](https://github.com/js8call/js8call) and
[Xastir](https://github.com/Xastir/Xastir). "Winlink" is a registered trademark of the Amateur Radio Safety Foundation, Inc.;
hamstack is an independent project.
