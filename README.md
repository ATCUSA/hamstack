# hamstack

Set up and manage amateur radio software (starting with Winlink) from one
tool: install packages, write configs, run services as systemd user units.
Early development; see the roadmap below.

## Quick start
Run `hamstack` in a terminal. On a fresh install it offers the guided setup before anything else (and the interface's welcome
screen starts it with Enter); decline once and it is not offered again, but `hamstack setup winlink` is always there.
The guided setup compares the distribution's hamlib and Direwolf with the newest release and lets you choose whether to build
the newer one from source (the default is the distribution's package, except when a radio needs the newer hamlib).

    go build -o bin/hamstack ./cmd/hamstack
    ./bin/hamstack setup winlink          # guided: radio, callsign, install, services, check, start
    ./bin/hamstack                        # the single-window interface (see below)

No radio? Choose the `dummy` radio profile to try everything without transmitting.

Individual commands (all documented in `hamstack help`):

    hamstack install [PROGRAM...|all] [--list] [--from-source] [--dry-run] [--yes]   # no names: choose from the list; shows every command first
    hamstack update [--dry-run]            # versions vs upstream; updates what hamstack installed
    hamstack versions                      # installed vs latest upstream
    hamstack doctor                        # checks with a fix for each problem
    hamstack units install | status | start | stop | logs ...

Requires a systemd user session. Install methods: hamlib from the distro package manager
(sudo), Pat from its upstream release tarball (sha256-verified, to `~/.local/bin`), Mercury
built from its tagged source (to `~/.local`). Tested in clean Arch, Debian 13 and Fedora 45
containers.

## The interface
Run `hamstack` with no arguments in a terminal. Everything you can do on the command line you can do here:

| Tab | What you do there |
|---|---|
| Services | start, stop, restart, enable at login, read logs, install the services (`U`) |
| Rigs | add a radio (pick the model, name it, choose a modem; its USB devices are detected), switch it on or off, change its modem, remove it, identify it by unplug and replug |
| Sites | save the current setup as a site, switch sites, set a site's grid, jump to the site whose radios are plugged in |
| Stations | add a callsign, import your existing Pat configs, switch station, remove |
| Devices | live list of USB serial ports and sound cards; one that appears while the interface is open is marked NEW; assign it to a rig |
| Programs | installed versus latest upstream; install, update, or build from source |
| Settings | Pat on/off, grid square, start at boot, serial and sound access, each radio's switches (xara's screen and monitor, Data2G's page); a folded **Advanced** section (shared listening, ardopcf's screen) and **Expert mode** (xara's registered speeds, Mercury's page on the network). Each setting says whether it is at its best-practice default; one that differs shows the default and **Back to default** (`D` in the terminal interface, `hamstack config changed` and `hamstack config default KEY` on the command line) |
| Doctor | every check, and `f` runs the selected check's own fix |
| Logs | the journal of one service; `[` and `]` change the service (or `l` on a service) |
| Network | every port the stack uses and who can reach it (see "Network") |

Keys `1` to `9` open the first nine tabs and `0` the tenth (Network); Tab and the arrow keys move between them.

Small operations happen inside it with prompts. Anything that needs a terminal or a password (installs,
`sudo`, the guided setup `w`, `rig identify`, a doctor fix) is handed the same terminal: you see exactly what
it prints, press Enter, and the interface comes back and refreshes. Before one of those runs, the prompt shows
the exact command, and nothing runs without a `y`. On a fresh install the first screen offers the guided setup.

## Rigs: one or several radios
A **rig** is one radio you own and how it is wired up: its model (profile), the serial port and sound
card it uses, an optional modem, its bands, and its own ports. Several can run at once, for example
an HF rig with Mercury and a VHF/UHF rig.

    hamstack rig add hf   --profile ic7300mk2 --modem mercury --bands hf    # detects its USB devices if plugged in
    hamstack rig add vhf  --profile ftdx10 --bands vhf
    hamstack rigs                          # what you have, and whether each radio is plugged in
    hamstack rig identify vhf              # unplug / replug to find (or re-find) its USB devices
    hamstack rig disable vhf               # keep it in the config but do not run it
    hamstack rig set hf modem none         # also: name, profile, bands, cat, audio
    hamstack pat on|off                    # Winlink client, shared by all rigs

`setup winlink` creates the first rig for you. Systemd units are templates (`hamstack-rigctld@hf`,
`hamstack-mercury@hf`) so adding a rig never needs new unit files, and they hold no device paths: each
runs `hamstack run`, which finds the radio's serial port and sound card when the service starts, using the
USB identity (vendor, product, serial number) saved with the rig. A radio on another USB port, or moved to
another site, keeps working.

Service names are `rigctld@hf`, `mercury@hf` (or `data2g@hf`, `direwolf@hf`) and `pat`. Commands take a service (`mercury@hf`), a kind
(`mercury`: every one), a rig (`hf`: everything of that rig) or `all`.

Each rig gets its own ports: rig control counts up from 4532, and a rig's Mercury uses a block of ten from
8300 (its web UI, broadcast and message history are separate too). Ports are kept when a rig is removed so
the others never move under Pat's config. Pat has one HF modem setting, so with several Mercury rigs Pat's
is wired to the first (Mercury or Data2G); `doctor` checks that it points at an HF modem that exists. Configs from before rigs
existed are migrated automatically.

## Sites: home, the EOC, the club
A site is a place you operate from. It records which of your rigs are there, which station (callsign) is
active, and the grid square, so moving between places is one command instead of a dozen.

    hamstack site add home                  # save what is set up now (rigs, station, grid) as "home"
    hamstack site add eoc --rigs vhf,dig --station eoc --grid FN31pr
    hamstack site use eoc                   # its rigs on, the others off, its station and grid; services restarted
    hamstack sites                          # your places, and how many of each one's radios are plugged in now
    hamstack site here                      # which site do the plugged-in radios match?  (--switch to go there)
    hamstack pat sync                       # wire Pat's config to the rigs and grid you are at now

The current site is a live view: enabling or adding a rig, or switching station, is recorded in it, and
`config set grid` changes the current site's grid (or the default when you have no sites). `site use`
restarts things only if the stack was running, and stops the rigs that are not part of the new site.

hamstack suggests a site but never switches by itself: `doctor` and `sites` say when the radios plugged in
belong to a different site. Radios are recognised by the USB identity saved with each rig, so a rig with no
saved device (the dummy radio) takes no part in the matching.

Moving to a new site changes the grid and the radios, but Pat's own config is never rewritten behind your
back. `doctor` warns when Pat's locator, HF modem or packet TNC no longer match, and `hamstack pat sync`
fixes exactly those settings (the locator, the rig connections, the VARA HF address, the AGWPE address),
keeping everything else, passwords included, and leaving a `.hamstack-bak` copy.

## Programs: pick what you want
hamstack knows more than the Winlink stack, and each program is installed, version-checked and updated the same way.

    hamstack install                    # in a terminal: the whole list, by category, to choose from by number or name
    hamstack install --list             # the same as a table: what each is, if it is installed, how it would be installed, what it needs
    hamstack install fldigi flmsg       # by name
    hamstack install wsjtx-improved --from-source   # build the newest release instead of the distribution's package
    hamstack versions                   # installed against the newest upstream release, for all of them
    hamstack update                     # update what hamstack built into ~/.local; says how for the rest

| Program | What | Newest release from |
|---|---|---|
| pat, mercury, hamlib, direwolf | the Winlink and packet stack | GitHub |
| flrig, fldigi, flmsg, flamp | the fl suite: rig control, digital modes, forms, file transfer | SourceForge (the GitHub mirror lags) |
| wsjtx, wsjtx-improved | FT8, FT4, WSPR... (WSJT-X Improved is the usual choice; they conflict) | SourceForge |
| js8call | keyboard messaging on HF | GitHub |
| xastir | APRS client with maps | GitHub |
| gpsd, ax25 | GPS daemon; the kernel AX.25 tools | the distribution |

**Dependencies and suggestions are tracked.** A program that cannot work without another (Mercury needs hamlib's rigctld) pulls it in
and says why; programs that merely go well together (fldigi with flrig, flmsg and flamp; Xastir with Direwolf and gpsd) are suggested, never
added without asking. Programs that provide the same thing (the two WSJT-X variants) refuse to install together.

**Where each comes from.** Debian and Fedora package all of them, but a distribution lags (Debian 13 ships Direwolf 1.7, WSJT-X Improved 2.8.0
against 3.2.1 upstream), so every program can also be built from its upstream source into `~/.local`
(`--from-source`; the guided setup offers it when the package is old). Arch has none of the fl suite, WSJT-X, JS8Call or Xastir in its repositories
(they are in the AUR), so on Arch those are always built from source: hamstack never runs AUR helpers or builds AUR packages.
Data2G (an HF data modem that speaks the VARA interface, so Pat can use it as VARA HF; both ends need Data2G) is a download, not a build:
hamstack fetches the project's release, checks its sha256, unpacks it to `~/.local/share/data2g` (it brings its own Qt and Hamlib, and needs glibc
2.39 or newer: Debian 13, Fedora 40, Ubuntu 24.04) and links the launcher as `~/.local/bin/data2g` (`data2g` opens the GUI, `data2g host` and
`data2g web` run the headless and browser front ends). It is also a rig modem: see "Data2G as a rig's modem" below.
Tested: installed from the real release on x86_64 and aarch64 and run (`data2g host --help`); not run on the air.

A source build shows what it will install and where it downloads from first. Programs that cannot print their version without a display (the Qt
applications) have it read from the package database, or from a record hamstack keeps for its own builds.

Every source recipe was built to completion on Debian 13, Fedora 45 and Arch (the fl suite, Xastir, JS8Call, ardopcf, WSJT-X Improved); the
official WSJT-X is built on Debian and Fedora only (Arch no longer ships a Qt 5 module it needs: use WSJT-X Improved there). The built programs were
checked to install and report a version, not run: the Qt programs need a display, and nothing was used on the air.

## VHF/UHF packet (Direwolf)
Direwolf is a soundcard packet TNC: it turns a radio's audio into AX.25 packets, and Pat connects to it
through its AGWPE interface. A rig can use it as its modem instead of Mercury:

    hamstack rig add pkt --profile digirig --modem direwolf --bands vhf   # a Digirig: audio + PTT on a serial line
    hamstack rig add 2m  --profile ftdx10  --modem direwolf               # a radio with CAT: keyed through its rigctld
    hamstack install direwolf          # distro package on Debian and Fedora; built from source on Arch
    hamstack install direwolf --from-source   # the newest release (Debian's package lags: 1.7 vs 1.8.1)

How the radio is keyed comes from its profile: `rig` (through rigctld, for radios with CAT), `rts` or `dtr`
(a serial modem-control line, which is how a Digirig works), `cm108` or `none` (VOX). An interface with
no CAT, such as a Digirig, has no rigctld at all. Each Direwolf gets its own AGW and KISS ports (8000/8001 for
the first rig, then 8002/8003, and so on) and the unused default KISS port is switched off so several can run.
One modem per rig: an HF rig with Mercury and a VHF rig with Direwolf can both run, but not both modems on one
radio. Pat has a single AX.25 setting, so with several Direwolf rigs it is wired to the first.

Configure a rig's Direwolf with `hamstack direwolf` (also `d` on the Rigs tab):

    hamstack direwolf setup             # guided: modem speed, callsign and SSID, key-up timing, digipeating
    hamstack direwolf set pkt baud 9600       # or one setting at a time: baud, call, txdelay, txtail, persist, slottime, digipeat
    hamstack direwolf show              # the settings, and the exact direwolf.conf it will run
    hamstack direwolf edit              # your own extra lines (beacons, an iGate...), appended last so they win

Settings you never touch stay at Direwolf's defaults (the configuration is byte for byte what it was before). Digipeating makes the station transmit
others' packets unattended, so turning it on prints a warning first.

Limits to know about: Direwolf listens on all network interfaces with no password (its AGW and KISS ports),
and it will not run as root. A hardware KISS TNC is not covered: Direwolf cannot act as a client of one, and
the generic route (Linux kernel AX.25 with kissattach) needs root and system configuration that hamstack does
not manage yet.

## GPS (gpsd)
A GPS receiver gives hamstack your grid square, so a portable station does not need it typed in at each site,
and gives Pat a position for its position reports. [gpsd](https://gpsd.io) owns the receiver and shares it
with every program that wants a position; hamstack runs one as a user service next to the others.

    hamstack install gpsd              # the distribution's package (not built from source)
    hamstack gps use                   # finds a receiver that is plugged in (u-blox and Garmin are recognised)
    hamstack gps use /dev/serial/by-id/...   # any other receiver: name it
    hamstack units install && hamstack start gpsd
    hamstack gps                       # service state, fix, satellites in use, position and grid
    hamstack gps grid                  # wait for a fix and print your grid square
    hamstack gps grid --save           # ... and save it for the current site (then: hamstack pat locator)
    hamstack gps use --addr HOST:PORT  # use a gpsd that already runs (a system gpsd, or another machine's)
    hamstack gps on|off|remove

hamstack's gpsd listens on port 2948 on this machine only, not gpsd's standard 2947, so a system-wide gpsd cannot
clash with it. The receiver is found again by its USB identity when you replug it, like a radio. It is started
read-only (`-b`): gpsd normally probes a receiver by sending it commands, and a device chosen by mistake (a
radio's CAT port) must not be sent anything; `gps use` also refuses a port that a rig already uses.
`pat sync` points Pat's gpsd setting at it (only the address; Pat's own switches stay as you set them).
`hamstack doctor` checks that gpsd is installed, the receiver is there and readable, and that it has a fix.

Debian and Ubuntu: the gpsd package also starts a system-wide gpsd (`gpsd.socket`) and a udev rule that hands GPS
receivers to it as they are plugged in. That gpsd then holds the receiver, and hamstack's cannot open it.
`hamstack doctor` warns when it is running; turn it off with
`sudo systemctl disable --now gpsd.socket gpsd.service`. Time synchronisation from GPS (chrony) is not set up:
it needs system-wide configuration.

Tested with a simulated receiver (NMEA written into a pseudo-terminal, read by a real gpsd under systemd on Debian,
Fedora and Arch). Not tested with a real receiver, so receiver recognition, the udev interplay above and
a cold start on a real device are unverified.

## Watching and calling on packet (no kernel needed)
With Direwolf running, hamstack can watch the air and make a connected-mode call itself, over Direwolf's AGW port:

    hamstack packet listen              # every packet Direwolf hears, time-stamped (like axlisten)
    hamstack packet call K1ABC          # connect and chat; "~." on a line or Ctrl+C disconnects (like axcall)
    hamstack packet call K1ABC WIDE1-1,WIDE2-1   # through digipeaters
    hamstack packet listen --addr HOST:PORT      # a TNC on another machine

No root, no ax25-tools, no kernel AX.25, and it works while Pat is connected (Direwolf serves several clients). Text from
the air is stripped of control and invisible formatting characters before it is printed, so a packet cannot move your cursor
or retitle your terminal; UTF-8 text is shown as it is. Ending a call (Ctrl+C, `~.`, or the end of piped input) sends the
disconnect and waits up to two seconds for Direwolf to confirm it. These two commands are unit-tested against a simulated
AGW server and run against a real Direwolf in the scenarios, but have not carried a contact over a real radio.

## The kernel's AX.25 stack (optional)
For the classic tools (`axcall`, `axlisten`, `ax25d`) or software that wants an AX.25 socket, hamstack sets up the Linux kernel
stack on top of Direwolf:

    hamstack ax25                       # kernel support, tools, /etc/ax25/axports, interfaces, what is missing
    hamstack ax25 setup                 # install ax25-tools, add the axports entry, load the modules (shows each sudo command first)
    hamstack ax25 attach                # kissattach: connects Direwolf to the kernel and creates ax0
    hamstack ax25 call K1ABC            # axcall through it        hamstack ax25 listen   # axlisten
    hamstack ax25 detach

The AX.25 port itself (its callsign and SSID, speed, packet length and window) is asked about by `hamstack ax25 setup`, with the speed following what
Direwolf runs at, and changed later with `hamstack ax25 port set call|speed|paclen|window VALUE` (the axports file is rewritten with sudo, keeping a copy).

It works by starting that rig's Direwolf with `-p`, which offers a KISS pseudo-terminal (`/tmp/kisstnc`) that `kissattach`
attaches to. Things to know:
- **Recent kernels have no AX.25 at all** (the module was removed). `hamstack ax25` says so plainly instead of failing; use
  `hamstack packet` there, or a kernel that still has it (on Arch, `linux-lts`). Debian and Fedora kernels include it.
- On Arch, `ax25-tools` and `ax25-apps` are only in the AUR. hamstack tells you and does not build AUR packages itself.
- Direwolf fixes the name `/tmp/kisstnc`, so only one rig's Direwolf can be attached. `kissattach` is attached to that terminal, so
  after Direwolf restarts run `hamstack ax25 attach` again; `detach` restarts Direwolf, because killing `kissattach` alone
  leaves `ax0` behind (Direwolf still holds the terminal).
- `axlisten` and `axcall` only know ports whose interface exists, so attach first.
- A hardware KISS TNC on a serial port (no Direwolf) is not covered yet: it is `sudo kissattach /dev/ttyUSB0 PORT` by hand.
- Tested on a real Debian 13 kernel in an incus VM; the containers share the host's kernel and skip those steps.

## Network
    hamstack network                    # also the Network tab (key 0) in the interface

Every port the stack uses (rigctld, each modem's ARQ and UI ports, Direwolf's AGW and KISS, Pat's web UI, gpsd), whether something
is listening, and **who can connect**: this computer only, or any computer on your network. That is read from what is really
listening (`/proc/net/tcp`), not from what the configuration expects. Mercury's web UI and Direwolf's AGW and KISS ports listen
on every interface with no password; they are flagged. It also shows this computer's addresses and the AX.25 kernel state.
hamstack cannot see your firewall rules.

### Who can reach Pat's web UI

**Through the dashboard (the default while the dashboard runs).** Pat's page has no login of its own and can send
mail as your station, so the dashboard serves it behind its own login: over HTTPS, on Pat's port plus 1000
(`https://HOST:9081/` for Pat on 8081), to dashboard logins only, and Pat itself then listens on this computer only.
The Apps page and Pat's own page link there. Turn it off in Settings ("Pat's page through the dashboard") or with
`hamstack config default pat-page` to go back; off, Pat follows the station's access below. Tested in unit tests;
there is no container scenario for it yet.

Without the dashboard, or with that setting off, each station has an access setting for its Pat web UI. The
default is `local`.

| Access | Pat listens on | Who can reach it |
|---|---|---|
| `local` | `localhost:PORT` | this computer only (reach it from elsewhere with `ssh -L 8080:localhost:8080 HOST`) |
| `network` | this computer's LAN address | other computers on the network, not through `localhost` |
| `all` | `0.0.0.0:PORT` | every interface, including `localhost` |

    hamstack station access n0call            # show it
    hamstack station access n0call network    # or: local, all
    hamstack station add club --callsign K0CLUB --access all

In the interface it is `v` on the Stations tab. Changing the active station's access rewrites the units and restarts Pat.
The Pat service runs `hamstack run pat`, which works out the address each time Pat starts, so `network` follows the computer's current LAN
address (a DHCP change, or a reboot onto another network) with no unit to rewrite. Early at boot it waits up to a minute for an address; if there
is none Pat listens on `localhost` only and says so in the log.
**Pat's web UI has no password of its own**: anyone who can reach the port can read and send your Winlink mail.
hamstack cannot see your firewall. Only the Pat web UI has this setting; rigctld stays on `127.0.0.1`, and Mercury's
UI and Direwolf's ports listen on every interface regardless (see the Network tab).

### Data2G as a rig's modem

Data2G is the second HF modem, an alternative to Mercury, chosen per rig. One modem per rig, so a rig runs Mercury *or* Data2G; two HF rigs can run one each.

    hamstack rig add hf --profile ic7300mk2 --modem data2g
    hamstack rig set hf modem data2g           # or mercury, direwolf, none; in the interface: m on the Rigs tab
    hamstack setup winlink --components rigctld,data2g,pat

It is a service like the others (`hamstack-data2g@RIG`, `data2g@RIG` on the command line), keyed through the rig's rigctld (Hamlib model 2) so
the CAT port has one owner, and it stops with it. `hamstack run data2g RIG` works out the radio's sound card each time it starts: it finds the card's
name in `/proc/asound/cards` and, with PipeWire, the names PipeWire gives the card's input and output. It asks `data2g host
--list-audio-devices` which devices carry one of those names, and passes their numbers (it ignores the "Monitor of ..." entries). Data2G lists
PipeWire's names there ("PCM2903C Audio CODEC Analog Stereo"), which share nothing with ALSA's ("USB AUDIO  CODEC"): with ALSA's alone, the card
was not found on a Raspberry Pi. Settings you make in Data2G's own GUI or web UI are kept per rig in `~/.config/hamstack/rigs/RIG/data2g.conf`, under
the flags hamstack sets (ports, rig, callsign, sound devices).

Ports come from the same per-rig block as Mercury's, so the first rig is the VARA HF default: command 8300 and data 8301 (Pat is wired to
the command port, as for Mercury, and `doctor` checks it), KISS 8100, web UI 8400, then +10 and +1 for the next rig. The VARA and KISS ports listen on
`127.0.0.1` only. With the modem web UI on (`hamstack config set ui on`, the Settings tab) the service runs `data2g web` instead of `data2g host`.

**Data2G's web page through the dashboard.** It is on by default; `hamstack config set data2g-page off` turns it off.
When the dashboard runs, it serves Data2G's page itself, over HTTPS and only to the dashboard's
logins, on a port of its own: Data2G's page port plus 1000 (`https://HOST:9400/` for the first rig, 9401 for the next). Data2G then listens on
this computer only, whatever its network setting says. The page's scripts use paths from the root of the site, so it cannot be moved under a path
of the dashboard; a browser sends a host's cookies to every port of it, so the dashboard's login is seen there. Your browser asks once to accept
the dashboard's own certificate for that port too. The dashboard's login cookie is never passed to Data2G, and the page's open connection closes
when you log out of the dashboard. Without the dashboard, the page listens on the network as its network setting says, without encryption.

Data2G keeps its page locked until it has a password of its own. Set it on the Radios page (Data2G: **Set page password…**, which needs the
dashboard password again, since the page can change Data2G's settings and key the radio), or with `hamstack data2g password [RIG]` in a terminal.
It is stored hashed in that rig's `data2g.conf`, and the running service picks it up without a restart. Turning the modem pages on from the
dashboard (Settings) also needs the dashboard password again.

Tested: the relay in unit tests (logins only, the dashboard's cookie kept from Data2G, writes and WebSocket from another site refused, the
WebSocket closed at logout); Data2G's page, login and paths read from a running Data2G on a Raspberry Pi.
To start it at login, `hamstack enable data2g` (with `hamstack linger on` on a headless machine, that means at boot).

## Web dashboard
    hamstack dashboard on [--port 443] [--cert FILE --key FILE]   # installs Xvnc and noVNC, asks before using sudo
    hamstack dashboard password                                   # nobody can log in until this is set
    hamstack dashboard                                      # state and address;  hamstack dashboard off

One HTTPS page for the whole station: every service with its state, start, stop and restart, links to the programs' own web UIs
(Pat, Mercury, Data2G), and desktop programs in the browser. Each program (WSJT-X, JS8Call, Fldigi, FLMsg, FLAmp, Xastir, FLRig) runs
alone on its own virtual display, the way DigiPi does it: TigerVNC's `Xvnc` on localhost, with IceWM (a standard window manager from your distribution, run as it is) so a program's extra windows such as WSJT-X's waterfall have frames and a taskbar, and the
dashboard relays noVNC in the browser to it. "Full desktop" shares the computer's own desktop the same way through a wayvnc of hamstack's
(localhost only), so the computer's own VNC server is left alone. The same programs from the command line: `hamstack app list|open|close`.

- **One way in.** The VNC servers listen on localhost with no VNC password; only the dashboard reaches them, after a login. The password
  is stored hashed (PBKDF2) in `~/.config/hamstack/web-password`; five wrong tries lock that address out for a few minutes. Sessions are
  HttpOnly, Secure, SameSite=Strict cookies; actions and the VNC relay refuse other sites.
- **The radio.** WSJT-X, JS8Call and Fldigi use the radio's sound card, so opening one stops the HF modem (Mercury or Data2G) and closing
  it starts the modem again. One radio program at a time. Set the program up for the radio yourself the first time: rig "Hamlib NET
  rigctl", `localhost:4532` (the rig's port in `hamstack rigs`), and the radio's sound card.
- **Certificate.** Give your own (`--cert`/`--key`, a chain is fine), for example one from a private CA renewed by `step ca renew`: the
  dashboard picks up a renewed certificate without a restart. Without one it makes a self-signed certificate for this computer's names.
- **Port 443** is below the range a user service may use, so `web on` lowers `net.ipv4.ip_unprivileged_port_start` to the dashboard's
  port in `/etc/sysctl.d/60-hamstack-web.conf`.
- Services: `hamstack-web` (in `hamstack start` and `status`), and on demand `hamstack-vnc@APP` (closes with the program) and
  `hamstack-desktop`.

## Dashboard pages, activity and logs

The dashboard has six pages: **Overview** (the radio cards and a glance at the services), **Radios** (one page per
radio: its controls, modems, radio setup, sound, Pat, its own switches under **Settings for this radio**, and its
ports), **Apps** (programs in the browser and the shared desktop; each program's own page has start at login and its
log), **Services** (every service: state, start/stop/restart, start at login, and a Log button), **Updates** (updates,
programs, system, maintenance, and the Activity section) and **Settings** (rigs, sites, stations, devices, network,
remote access, GPS, checks). Each setting has one home: the Settings tab keeps what is station-wide and lists where the
rest are set (a radio's page, Sites, Remote access, Updates). Set things show as a line with a ✎ button: the form is
there only when you ask for it.

The Apps page shows the programs that are installed (Pat included: it has a web page of its own, so its card links
there), with a box to show the ones that are not, each with an **Install** button. Each installed app has a ⚙ to its
own page, `/apps/NAME`: its status and controls, **start at login**, a few settings hamstack manages by name, and a
**recommended setup** shown as a before/after table first. *Apply* makes only those changes (the original file is
kept once, as `*.hamstack-bak`), and *Undo* puts back only the settings that still hold what hamstack gave them.
For Pat the recommended setup wires its rig connections, grid, HF modem and packet TNC, and names a rig for ARDOP
**only where Pat has none**. The page never shows Pat's config file, only the named settings (the Winlink password is
never read), and `hamstack pat sync --dry-run` prints the same plan in a terminal. `hamstack setup` applies Pat's
recommended setup by itself and prints what changed; the Pat page's Undo puts it back. For WSJT-X, JS8Call and fldigi
the recommended setup points the program at the rig's rigctld.

hamstack also applies it by itself when one of them starts from the dashboard or a hamstack menu entry, but only where
nothing is set yet: a program with no radio gets the rig's rigctld, and WSJT-X with no sound device gets the radio's
card. A device or radio you chose is never changed by itself, and nothing is written while the program is already open.
*Undo* on a program's page also stops it being set up by itself again, until you press *Apply*. Settings, "Set up radio
programs when they start" (`hamstack config set app-setup off`) turns it off for all of them.

For WSJT-X it also checks the sound. It offers to set input and output to the radio's sound card (as PipeWire names
it), and to turn off **TCI audio**, which sends the audio over the network to radios that support TCI. With any other
radio, TCI audio on means WSJT-X keys the transmitter and sends nothing. On a Raspberry Pi with an FT-DX10 it read 5 W
with no ALC on every over, and nobody heard it. With TCI audio off it read 52 W, and PSK Reporter had 328 reports in
four minutes. Apply is refused while WSJT-X is open, because it saves its own settings when it closes and would put the
old ones back. By itself, at start, hamstack only fills sound devices that are empty and leaves TCI audio as it is
(it is right for a TCI radio): the Apply button turns it off. Not done for JS8Call: its build is Qt 5, and its device
names were not checked.

ARDOP also needs an ARDOP modem listening where Pat's `ardop.addr` says (ardopcf uses `localhost:8515`). hamstack
runs Mercury, Data2G and Direwolf and cannot start an ARDOP modem yet, so a rig named for ARDOP lets Pat tune and key
the radio but not connect.

Updates, builds from source and system upgrades run as **jobs** in the background, one at a time. A job is the same
`hamstack ...` command you would type, run as a child process, so everything it and the programs it starts print
is captured to a file under `~/.local/state/hamstack/jobs`. The Activity section shows each job's progress and a
**Details** terminal with coloured output; a job that can be stopped has a Stop button, and one that must not be
(a system upgrade) does not. While a job runs, the dashboard refuses to restart itself, restart or shut down the
computer, so a package manager is never cut off part way. Tick several programs on the Updates page and press
*Update selected* to update them in one job.

A job that needs `sudo` cannot ask for a password from a page. If a plan needs administrator rights and sudo would
ask, the page says so and gives the exact command to run in a terminal instead. (`hamstack system allow` covers the
system update and the power commands; it does not cover installing build packages.)

The terminal is one reusable component: it can show a job's **Terminal** output or a service's **Logs**, with the two
switchable in one view. Logs are read with `journalctl --user-unit`, which also works where the journal is not kept
per user.

## Desktop launchers

On a computer with a desktop installed (a graphical session in `/usr/share/xsessions` or `wayland-sessions`, so it
works when setup runs over SSH; IceWM's, which the dashboard brings for its browser displays, does not count), `hamstack setup` puts entries in the application menu by itself, and installing a
program adds its entry: the dashboard, the terminal interface, and each installed program. `hamstack launcher remove`
(or Settings, "Menu entries on this computer's desktop") takes them away and keeps them away; `hamstack launcher
install` puts them back. A program's entry runs `hamstack launch PROGRAM`, which sets up
its radio the way the dashboard does, makes room on the sound card (the HF modem stops while a radio program is
open, and starts again after) and starts it. Its second action, *Launch with debug output*, opens a terminal showing
what the program prints; a copy always goes to `~/.local/state/hamstack/launch-PROGRAM.log`. The entries are
recorded and `hamstack uninstall` removes them.

The radio programs' own menu entries (WSJT-X, JS8Call, Fldigi) start them without stopping the modem. The program
then opens while the modem still has the sound card, and has no sound. So for a program that gets a hamstack entry,
its own entry is hidden: one in a system folder by a user entry of the same name, one in your own folder (a source
build puts it there) by marking it hidden, keeping a copy. `hamstack launcher remove` and `hamstack uninstall` put
them back.

## One way to talk to the radio: rigctld

hamstack runs one rigctld per rig and everything else is a client of it: Mercury, Data2G, Direwolf's PTT, Pat,
and the programs you open from the dashboard. Only rigctld opens the radio's CAT port, so the programs never
fight over it. FLRig is not part of that chain. (Hamlib can use FLRig *under* rigctld, but a program cannot be a
client of both at once, so hamstack does not offer switching between them.)

### The radio panel

The dashboard shows a **Radio** card for each rig that has CAT control: frequency, mode, power, band buttons,
and live meters read through rigctld (S meter while receiving; power, SWR, ALC and compression while
transmitting). Only what the radio reports is shown. The panel reads about once a second, only while the page is
open, and the transmit meters only while the radio transmits, because Mercury shares the same serial line.

- Changing frequency, mode or power is refused while the radio transmits.
- The band buttons go to the band's digital calling frequency, in PKTUSB if the radio has it (USB if not), and
  then remind you to tune.
- **Tune** runs the radio's own antenna tuner (hamlib's `TUNE`; on Yaesu radios that is the `AC002` command), at
  10% power, then puts your power back. It **transmits a carrier for a few seconds**, so it is off until you turn
  it on: `hamstack dashboard tune on`. It refuses unless it can see the radio is not already transmitting and can read
  and lower the power; it can be stopped from the page; and a cycle that has not ended after 25 seconds is
  stopped. Turn the tuner on only with a suitable load and a clear frequency.

### Programs come configured for the radio

When you open WSJT-X (Improved), JS8Call or fldigi from the dashboard and the program has no radio set up yet,
hamstack fills in the rig's rigctld (`Hamlib NET rigctl`, `127.0.0.1:PORT`, PTT by CAT) together with your
callsign and grid. A program that already has a radio, or an fldigi settings file, is left exactly as it is, and
the WSJT-X and JS8Call settings are copied to `*.hamstack-bak` first. The rig chosen is the first one whose
rigctld is answering; if none is, nothing is written and the next start tries again. Sound card choices are not
filled in: they cannot be worked out reliably, so pick them in the program once.

Each program was started against a dummy rigctld and read its frequency and mode from these settings. Keying was
not tried: look once at the PTT method (WSJT-X and JS8Call: Settings, Radio; fldigi: Configure, Rig control,
Hamlib) before you transmit. `hamstack uninstall` takes the radio keys out again, only while they still hold what
hamstack wrote, and leaves the rest of the program's settings alone.

## Choosing a radio
The guided setup (and the Rigs tab) shows a short list first: the dummy radio (testing without hardware), a Digirig, eight common rigs (IC-7300,
IC-7300 MK2, IC-705, IC-7610, FT-991A, FT-710, FT-DX10) and FLRig (control through flrig). Anything else is one search away: `s`, then type part of
a name (`kx3`, `ic 7100`, `yaesu`). The list is every radio the *installed* hamlib supports (it offers to install hamlib first), matched
loosely (spaces and dashes ignored, letters in order), paged. A radio without a profile gets one made and saved on the spot, asking the serial speed
(it shows the range hamlib says the model supports). Profiles other than the dummy, Digirig, IC-7300 MK2 and FT-DX10 were written from hamlib's list and
typical settings, not tested with the radio: check the CAT rate on the radio matches.

With a radio that is not plugged in, setup finishes and says which services will wait; they show **no radio** (not a failure) and start as soon as it is
plugged in and you run `hamstack start`. A Digirig with Mercury switches that rig to Direwolf (it has no CAT control to key Mercury with) and says so.

## Finding your hardware
    hamstack devices          # USB serial ports and sound cards, grouped by shared hub
    hamstack devices watch    # live: shows each device as you plug it in or unplug it

A radio's CAT port and its sound card normally hang off one USB hub, so they are listed together
(a radio like the IC-7300 MK2 shows both of its serial ports and its codec in one group). A hub on
your desk looks the same as one inside a radio, so a group is a hint, not a promise. Devices are
named by USB vendor:product and serial number, which survive replugging and other USB ports.
Discovery reads sysfs and needs no privileges (unlike dmesg).

## Permissions
Opening a radio's serial port needs a system group (`uucp` on Arch, `dialout` on Debian and
Fedora); services running with nobody logged in (linger) also need `audio` for sound cards.

    hamstack groups           # where you stand: account, this login, and your service manager
    hamstack groups fix       # adds you with usermod, showing the command first

`setup winlink` offers this. Adding a group only helps services after your login and the user
service manager restart: log out and in, then `sudo systemctl restart user@UID.service` (or
reboot). `doctor` reports which of those is still pending.

## Radio support check
`doctor` asks the installed `rigctld` whether it has a driver for your radio. Distributions
often ship an older hamlib than a new radio needs (Debian 13's hamlib 4.6.2 has no
IC-7300 MK2 driver; 4.7.2 does), and the check says so instead of letting rigctld fail later.

The fix is a source build of the latest hamlib:

    hamstack install hamlib --from-source

It shows a disclaimer first (it installs compilers and development libraries, takes a while,
and leaves those packages installed), verifies the release tarball's checksum, builds in a
temporary folder and installs to `~/.local`. Your distribution's hamlib is left alone and
hamstack prefers the newer copy even if `~/.local/bin` is not first on PATH. `setup winlink`
offers this when your radio needs it; an unattended run (`--yes`) only does it with
`--allow-source-build`. `hamstack update` rebuilds a source-built hamlib the same way.

## Stations (several callsigns)
A *station* is an operating identity: a callsign with its own Pat config, mailbox and
web UI port, for example a personal call, a club call and an emergency-communications call.
Stations are yours to define; none are built in, and they are kept in `~/.config/hamstack/`.

    hamstack station add club --callsign K0CLUB --name "Club"   # starter Pat config created if missing
    hamstack station import-pat                                 # adopt existing ~/.config/pat/*.json as stations
    hamstack stations                                           # list
    hamstack use club                                           # switch: regenerates config, restarts mercury + pat

Radios (profiles) and stations are independent: any station works with any radio.
hamstack never stores your Winlink password; it lives in the station's Pat config.
Only one station is active (and can use the radio) at a time. Pat logs go to
`~/.local/state/hamstack/`.

## Services
systemd user units: `hamstack-rigctld@RIG`, `hamstack-mercury@RIG`, `hamstack-data2g@RIG`, `hamstack-direwolf@RIG`, `hamstack-pat` and, with a GPS, `hamstack-gpsd`:
- mercury and data2g `BindsTo` rigctld (stopping rigctld stops them) and wait for rigctld's port before starting;
- pat `Wants` the HF modem (mercury or data2g);
- the pat unit follows the active station (its config file and web UI address);
- a missing radio makes rigctld exit once with a clear message (no restart loop);
- the CAT serial device is resolved when the service starts, so replugging is fine.
Mercury and Data2G use port 8300 for the first rig, the same as VARA HF: do not run any two of them on it.

## Keeping software current
`hamstack versions` compares every program's installed version with the newest upstream release (GitHub, or SourceForge for the fl suite and
WSJT-X, whose GitHub mirrors lag), and `hamstack update` brings up to date what hamstack itself put in `~/.local`. How each is updated depends on where
it came from:

| Source | Update by |
|---|---|
| Distribution package (pacman, apt, dnf) | your package manager; hamstack shows the command and says when the distribution's version lags upstream |
| Upstream release (Pat) | downloaded, checksum verified (the sha256 GitHub publishes), installed to `~/.local` |
| Built from source (Mercury, Direwolf on Arch, anything with `--from-source`) | rebuilt from the newest upstream source into `~/.local`; a version record is kept for programs that cannot print their own |
| AUR (Arch) | not touched: hamstack never runs AUR helpers or builds AUR packages; where the AUR is the only source it builds from upstream instead, or says so |

A distribution package may lag upstream; hamstack reports both and says which one an update would actually give you.

## Uninstalling

`hamstack uninstall` removes hamstack and undoes what it changed on the computer. It shows every step first
(`--dry-run` shows them and stops).

It asks one question: keep the ham radio programs hamstack installed, or remove those too
(`--keep-apps`, the default, or `--remove-apps`).

- **Always removed:** hamstack's services, its settings and state folders, the packages it added only for
  itself (TigerVNC, noVNC, nftables), the root-owned files it wrote (the sysctl file for a low dashboard port,
  the firewall helper, its service and sudo rule), and hamstack itself. Lingering is switched off again if
  hamstack switched it on.
- **Removed only with `--remove-apps`:** the programs it installed (packages, and files under `~/.local`), the
  compilers and libraries it built them with, the AX.25 port file and module settings, and your membership of
  the serial and audio groups if hamstack added it.
- **Never removed:** the programs' own data, such as Pat's mailbox and settings, logbooks and contacts. Each
  station's Pat settings are moved from hamstack's folder to `~/.config/pat/hamstack-ID.json` first.

hamstack knows what to undo because it writes each change down as it makes it, in
`~/.local/share/hamstack/changes.json`: only real changes, so a package that was already installed or a group
you were already in is left alone. A package that something else has since come to depend on is also left,
and named. When the apps are kept, the record of them stays, so a later `--remove-apps` still works.

Changes made by a version from before this record existed are found where they can be recognised (the files
hamstack names itself). Packages and programs installed back then are not removed: hamstack cannot tell them
from ones you installed yourself.

A Raspberry Pi with RealVNC installed keeps it: RealVNC's `Xvnc` is not what hamstack uses, so
`hamstack dashboard on` installs TigerVNC beside it and uses `Xtigervnc`. The Pi's own VNC server is not touched.

## Testing
    go test ./...                              # unit tests, a few seconds

    scripts/incus-test.sh                      # the real thing: Debian and Fedora under real systemd
    scripts/incus-test.sh --distro arch        # also: arch (debian, fedora, arch)
    scripts/incus-test.sh --only multirig,sites,direwolf
    scripts/incus-test.sh --fresh              # start from brand new instances
    scripts/incus-test.sh --cleanup            # delete the test instances

`incus-test.sh` needs [incus](https://linuxcontainers.org/incus/). It builds hamstack, creates one
system container per distribution (named `hamstack-test-<distro>`, and it never touches any other
instance), and runs `scripts/incus-scenarios.sh` inside each as an unprivileged user with `sudo`, the way a
real user would. The distributions run in parallel, each with a full log, and you get a summary.

The scenarios (about 190 assertions per distribution) cover: installing the programs; the guided setup;
several rigs at once (their own ports, stopping, disabling, removing); sites and switching between them;
migrating a config and units from the single-radio versions; Direwolf with a CAT radio and with a Digirig
that is not plugged in; doctor (including the old-hamlib check); the group and linger handling across a
new login and a restarted service manager; `pat sync`; a GPS (simulated receiver, a real gpsd); packet listen and call against a real Direwolf; the kernel AX.25 stack (only where the kernel has it: run it in a VM); a reboot (services must come back by themselves);
and the interface driven through tmux. The first run downloads images and builds Mercury, so it takes
several minutes; later runs reuse the instances and take about six.

## Roadmap
1. Core, profiles, `versions`, `doctor` (done)
2. systemd user units: rigctld, Mercury, Pat, `status`/`start`/`stop`/`logs` (done; tested with the dummy profile only)
3. `install`/`update`, guided `setup winlink`, TUI (done; no real radio tested yet)
4. Direwolf, Digirig and KISS packet
5. Fedora, release pipeline, one-line installer


## Browser management and remote access

The CLI, TUI and browser use the same configuration, backend operations, network policies,
installation plans and release inventory. The web control center includes rigs, stations,
sites, USB devices, programs, settings, network access, checks, logs, GPS, remote access,
updates and maintenance. Operations that need an interactive terminal are available through
an explicitly enabled browser console, so their existing guided flow and sudo prompts can
be used without a separate implementation.

```sh
hamstack dashboard check
hamstack dashboard on --access local                 # authenticated HTTPS, generated self-signed certificate
hamstack dashboard on --desktop --access network     # also install compatible whole-desktop sharing
hamstack dashboard on --cert /path/cert.pem --key /path/key.pem
hamstack install vnc novnc                     # remote-access components are also in the catalog
hamstack app open wsjtx --browser
```

Guided setup offers the dashboard and optional desktop sharing. The TUI Settings tab can
set them up later. Individual apps run on their own TigerVNC displays; Xvnc and Xtigervnc
executable names are supported. Existing X11 desktops use x0vncserver (or x0tigervncserver),
and compatible Wayland desktops use wayvnc. GNOME, KDE/Plasma and Weston are reported as
unsupported by wayvnc. A Wayland session still needs the required compositor capture and
input protocols. Headless machines can run individual app displays without a full desktop.
The dynamic dashboard shows installed apps, current sessions and readiness checks.

A generated self-signed certificate is the starting default, so a browser will ask whether
you trust it. Custom PEM certificate/key files are supported now and renewed files reload
at the same paths. Automatic ACME provisioning is a future option, not currently built in.

For advanced setup, the Remote access section can review installation of xterm and enable
**Hamstack console**. This is off by default. When enabled, every logged-in dashboard user
can operate the CLI/TUI and a shell as the OS user running hamstack, including interactive
privilege prompts. It starts in the TUI; quitting leaves a shell. Ordinary web install
forms use noninteractive sudo and report when local authorization is needed instead of
waiting invisibly for a password. There is no arbitrary command endpoint in the HTTP API.

VNC servers stay on loopback. Browsers connect through the authenticated HTTPS WebSocket
relay. Native VNC viewers can connect through an SSH tunnel to the localhost port printed
by `hamstack app open APP`; raw unauthenticated VNC is not exposed to the network.

## Network access and applying changes

```sh
hamstack network access                       # list endpoint IDs and saved preferences
hamstack network access web network --restart
hamstack network access pat local --restart
hamstack network access data2g@hf/web network --restart
hamstack network firewall install --dry-run    # inspect the privileged helper's plan
hamstack network firewall install             # confirm before installing it
hamstack network access mercury@hf/8300 local --firewall --restart
```

The Network tab uses `v` to configure the selected endpoint and `F` to review firewall
setup. The browser has matching selectors. Each change offers a restart, and site-specific
choices are saved with the active site. Native binds use loopback, the currently detected
LAN address, or all IPv4 interfaces. Network-mode native listeners restart their child
when LAN addresses change; missing LAN addresses fall back to loopback.

For servers without a suitable native bind setting, an optional nftables helper restricts
the TCP port. Its installation plan shows the root-owned helper, watcher service, approved
port list and narrow sudo permission. The helper only updates the invoking user's hamstack
firewall table and validates input. Other firewall tables remain intact and may still
block allowed traffic. Approving additional ports requires reviewing the helper installation
again. Existing installed dependencies are kept.

Firewall `local` permits loopback only. `network` also permits peers in the directly connected
IPv4/IPv6 prefixes, using currently assigned destination addresses. Prefixes refresh every
five seconds. Loopback remains allowed for internal radio clients in every mode. `all`
removes hamstack's extra filter; the application must also be listening beyond loopback.
The filter affects every listener on the selected TCP port. The Network page distinguishes
the observed listener from the saved preference instead of claiming an unverified policy
is active.

Serial and audio groups are both offered during setup. Pending configuration changes,
replaced running executables, manager reloads, stale group sessions and OS reboot markers
are surfaced through `hamstack maintenance` and the interfaces. Service restart prompts
warn that active radio sessions are interrupted. Reboot always requires explicit confirmation
and is never implied by an install's `--yes`.

## Release checks and verification

TUI and web startup check releases for installed programs and hamstack, then poll hourly.
`hamstack updates`, the Programs/Updates panels, `hamstack versions` and the update planner
share the release inventory. Suggestions identify already-installed components, and adding
all suggestions adds only missing ones. Distribution-managed software is identified with
its package-manager update route.

```sh
hamstack updates
hamstack update --dry-run
hamstack updates self --dry-run
hamstack updates self
```

Self-updates use published assets from `ATCUSA/hamstack` matching the platform. An asset's
published SHA-256 digest is required, and the previous executable is retained as
`hamstack.previous`. No release, unsupported asset formats or a missing digest produce an
explanation rather than an unverified replacement. Development builds warn that a release
may not include unpublished changes. This feature does not publish releases automatically.

For a localhost-only simulated dashboard, with no real service or host mutations:

```sh
HAMSTACK_WEB_PREVIEW=1 go test ./internal/web -run '^TestBrowserPreview$' -v -timeout 11m
# In another terminal, use the printed BROWSER_PREVIEW_URL:
node scripts/web-smoke.mjs http://127.0.0.1:PORT
```

The smoke test executes rendered page scripts against the preview API and visits every
management section. It is a DOM simulation, not a substitute for visual browser inspection.
