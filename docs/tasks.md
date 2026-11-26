# Task list

## What every agent must know (hamgo and integrations; from `docs/architecture/hamgo-and-integrations.md`, the plan of record)
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
**Audio:** the thin audio monitor was removed from v1; v2 gets rigctlWeb's full one through hamgo on the plan's "move day".

Kept up to date as we go: what is not working right, what needs verifying, what to improve, and what is planned. Newest
decisions at the top of each section. Mark done with a date; do not delete history.

## Release of the current version (branch `release/v0.3.0-prep`, local only, nothing pushed)
State 2026-11-25: the branch is one commit on top of `origin/main`; `task check` is green. Still to do before the push: atomic Pat config writes and the rest of the 2026-11-25 review list; verify the Pi fixes and hardening on the Pi. Done since: the sudo password no longer reaches source builds, sudoers rules are checked before they go live.
State 2026-11-15: the local work (69 commits, written with no AI credit lines) is merged with `origin/main` (v0.2.0 line, which had no common history with it) under the published module path `github.com/ATCUSA/hamstack`; the local code wins on conflicts; main-only files (site, workflows, LICENSE, SECURITY, docs, Taskfile) are kept; main's old `dashboard.html` is removed; the thin audio monitor is removed (v2 brings the full one through hamgo). Build, vet and all tests pass. Dependency bumps that main had (x/sys 0.50.0, x/ansi 0.11.9) are not in this go.mod (the local one is older): re-run the dependency update before the tag.
Before tagging: the owner verifies the "Needs verifying" list below on the Pi; the owner decides push, pull request and the tag (suggest v0.3.0); `backup-before-credit-strip` and `original/...` refs and the old branch stay local.
Then: v2 starts as a new sibling project with hamgo staged locally (owner decision), following `docs/architecture/hamgo-and-integrations.md`.

## Needs verifying (built, never seen working)
- [ ] (2026-11-25) On the Pi:
  - "Use on RIG instead of MODEM" on Services.
  - The dashboard-password prompt for console, tune and password changes.
  - The Pat note after a modem change.
  - The busy-card message in `journalctl --user -u hamstack-ardopcf@ftdx10`.
  - The update banner clearing after an update.
- [ ] Password dialog for installs, system update, reboot and shutdown: only tested with a fake `sudo`; try a real password, a wrong one, and Cancel.
- [ ] Apps page "Available to install" section and Install selected (never seen in a browser).
- [ ] Services page "Installed, not in use" and "Use on <rig>" (set up `ardopcf` on ftdx10), then start it.
- [ ] `ardopcf`: bind address (`ss -ltn`; if it listens on all interfaces it needs a network-access row), Pat sync dry run shows the ARDOP address and PTT change, unkey-on-stop unit hook (`hamstack run unkey`), ARDOP session end to end. Never started by me; connecting transmits, so the owner does that.
- [ ] Tune result message (final SWR) on a real tune.
- [ ] Logo in the tab, header and desktop menu icon (`hamstack launcher install`).
- [ ] Action feedback (busy button and toast) on every page: only the script syntax was checked.
- [ ] Slow-read pause for optional rig reads: committed, not yet on the Pi.

## Not working quite right
- [x] 2026-11-15: dashboard logins survive a restart (private file, cookies stored hashed, ended by a password change). The first restart onto this build still logs out; verify the one after. Flaky: `TestOnlyOneJobRunsAtATime...` can time out when packages test in parallel.
- [ ] `hamstack rig list` does not exist (the command to list rigs is under `hamstack rig`; check the help).

## Multi-window apps (decided 2026-11-15: IceWM, native defaults)
Decision: run IceWM (official package on Arch, Debian and Fedora; no config of ours) on each app display, plain `icewm` only (never `icewm-session`, which runs the user's own startup script). Matchbox is no longer installed (it is not in Arch's official repos, so Arch and Fedora had no window manager at all); a machine that already has it keeps working. Built and committed; still to verify on the Pi: install `icewm` (Settings, Remote access), open WSJT-X, check the waterfall window has a frame and a taskbar button, and noVNC scaling.
Original notes follow.

Problem: some programs open extra top-level windows next to the main one. They do not need to stay open.
Options, simplest first:
1. Use a floating window manager with titlebars and a taskbar/window list (JWM, IceWM, Fluxbox or Openbox + a panel) for apps that have more than one window, keep matchbox for single-window ones. Per-app setting `Windows: single|multi` in `internal/apps`.
2. Keep one window manager but add a small hamstack toolbar in the page (list the windows via `xdotool`/`wmctrl`, raise/close). More code, no new dependency on the display.
3. Make the virtual display larger and let windows float.
Recommendation: option 1 with JWM or IceWM (small, has a task list). Needs the choice, a catalog entry, and a test with WSJT-X on the Pi.
Open: window sizes (Wide Graph on the same screen vs bigger virtual display), closing windows, noVNC scaling.

## Planned
- [ ] **Defaults on, changeable in Settings; risky ones behind Advanced/Expert** (owner decision 2026-11-26: "a lot of the options you offer should be default settings with the option to change in settings, even if that means advanced/expert modes for potentially dangerous settings. That holds true for everything"). Settings get three levels: the normal page, an **Advanced** section (folded) for what changes what goes on the air or who can reach the station, and an **Expert** switch for licence- or shell-level things; the owner-only rule (dashboard password again) stays on everything that can transmit or expose. Inventory with the proposed default:
  - **On by default (normal Settings):** xara's screen (view only, localhost, behind the login); Data2G's page through the dashboard when the dashboard runs; desktop menu launchers, hiding the radio programs' own entries, on every install with a desktop; the PipeWire check and wait before radio programs; the recommended setup for WSJT-X/JS8Call/fldigi applied by itself when the program has no radio or sound set (a person's own choices are still never changed); Pat's recommended setup applied at setup; the transmit watchdog (its 30 s limit in Advanced); refusing a modem switch while Pat connects.
  - **On for new rigs (Advanced to change):** shared listening on the sound card.
  - **Advanced, off by default:** ardopcf's screen (its buttons can make the radio transmit; needs the firewall helper); Mercury's web page until it is served through the dashboard; the radio settings check fixing transmit-side settings (RPORT GAIN, power); the receive-side ones (AGC AUTO) fixed by itself.
  - **Expert, off by default:** xara's registered (VARA licence) speeds; the browser console (a shell); the dashboard Tune button (transmits a carrier); network exposure beyond this computer for modem ports.
  - **Decided by the owner (2026-11-26):** ardopcf's screen is on by itself whenever the firewall helper holds its port to this computer (Advanced to turn off); the radio settings check fixes receive-side settings (AGC AUTO) by itself when rig control starts, shows transmit-side ones (RPORT GAIN, power, data mod source) with a Fix button, and Advanced can make those automatic too; Settings get a folded Advanced section on each page plus one owner-only Expert mode switch.
  - Steps: (1) the defaults themselves (three-state settings: unset means the default); (2) Settings tiers (Advanced, Expert); (3) the radio settings check with the openRMS-Rigs profiles; (4) recommended setups and launchers applied at install and first run. All four built (step 4 on 2026-11-26).
  - Step 4 as built: WSJT-X/JS8Call/fldigi started from hamstack get their radio, and WSJT-X its sound card, where nothing is set (never a person's choice, never while open; Undo keeps it off for that program; `app-setup` setting); Pat's recommended setup at `hamstack setup`; menu entries at setup and after each install on a computer with a desktop session installed (`launchers` setting; `launcher remove` sticks); setup no longer asks about Data2G's page (it comes through the dashboard).
  - Profiles per program (owner asked 2026-11-26: "shouldn't you get to pick the profile for the different modes ... or at least have an easy way to know what one to pick"): each program has a recommended profile (ardopcf → Winlink ARDOP; xara, Mercury, Data2G → Winlink VARA; WSJT-X, JS8Call → FT8; fldigi → PSK31), changeable per rig on the Radios page or `rig set RIG radio-profile PROGRAM=PROFILE`; the check uses the profile of the program that has the radio (a radio program while open, else the modem), and its fixes run when WSJT-X/JS8Call/fldigi start from hamstack too. Fix only for the profile in use. Seen on the Pi 2026-11-26: WSJT-X started from the dashboard took the radio from xara, checked it against the FT8 profile (17 settings read, nothing to fix) and the owner made an FT8 contact; xara's check on its return used the VARA profile. Not seen: the AGC OFF to AUTO fix at a program's start (the radio's AGC was not OFF when either check ran; the same fix was seen for the modems earlier). Not yet: the TUI has no picker (CLI and dashboard only).
- [ ] **Dashboard walkthrough fixes** (walkthrough on the Pi 2026-11-26; owner decisions the same day). Order: (1) rig editor fix (done: one modem list, `config.ModemChoices`; the dashboard's editor lacked xara and ardopcf, so saving a rig running one stopped it); (2) one home per setting and consistent wording (done); (3) WSJT-X on the real radio (done 2026-11-26 after Apply on its page turned TCI audio off: an FT8 contact).
  - Built 2026-11-26: per-setting defaults (`config.SettingDefaults`, Back to default, `D`, `config changed|default|reset`); full reset; a radio's switches and the Tune button on its Radios page, Settings lists the other homes; Services lists services only; one wording for switching, start at login, Open; effective reach on Network and the Radios page; Pat behind the dashboard login by default (seen on the Pi); restart markers only for what a service reads; start-at-login warning for a second radio program. Incus Debian setup/network/apps/modemswitch 84/84.
  - Next: Mercury's page through the dashboard the same way (needs Mercury running to see it); flrig's XML-RPC held to this computer; FLRig under the flrig profile (start flrig before rigctld); a container scenario for the page relays (the dashboard password is asked on a terminal, so the scenario needs a way to set it).
  - **One home per setting:** remote access (dashboard password, HTTPS, whole-desktop sharing) in Settings, Remote access; restarts, start at boot, groups and updates on the Updates page; the grid on Sites; per-radio switches (xara's screen and monitor, shared listening, transmit-side fixes, ardopcf's screen, Data2G's page password, the Tune button) on the radio's page; start at login on each program's page; Pat shown once. Other places link to the home instead of copying it.
  - **Wording:** one verb for switching modems, one form for start at login, "Open" for opening a program, and every on/off switch saying which way it is now.
  - **Start at login for every app** (owner: "all apps should have the options to start"), radio programs included.
  - **xara's monitor off by default** (owner, 2026-11-26; was on), turned on in Settings.
  - **Best-practice defaults, each one revertible:** every setting that differs from its default gets "Back to default"; and one "Reset hamstack to its defaults" for the whole of hamstack's own settings (shown first, confirmed; never the Winlink password or other programs' own files beyond what hamstack documents).
  - **Access to every web page and port:** for each one, who can reach it (this computer, the local network, any network / 0.0.0.0), enforced by its bind address or the firewall helper, and optionally a login in front: the dashboard's own login (one central set of logins) for programs that have none, Pat first; logins set from the terminal or the dashboard. Show what is actually listening, not the setting alone (the Radios page showed xara's localhost ports as reachable by "anyone").
  - **FLRig** (researched 2026-11-26 from flrig 2.0.12 and hamlib source; not tried on a radio): flrig cannot use rigctld (only its own drivers, raw CAT over TCP, or another flrig), so the one way to share is flrig owning the serial port and rigctld as its client through hamlib's FLRig backend (model 4, `127.0.0.1:12345`, the `flrig` radio profile hamstack already has): frequency, mode, VFO, split, PTT, RF/AF/mic gain and power; AGC read only; raw commands through `rig.cat_string`. To build: under the flrig profile, start flrig before that rig's rigctld; under any other profile, say FLRig cannot share the port (rigctld holds it exclusively); a profile-dependent "controls the radio" mark separate from the sound-card one. flrig's XML-RPC (12345) listens on every interface with no login and can key the radio: hold it to this computer (firewall) by default.
  - **Full reset** (owner chose 2026-11-26: settings and access rules): every setting back to its default and every port's access rule back to the safe default (this computer only, never wider); rigs, stations, sites and the dashboard password stay; a backup of the config first and the list of changes shown before it runs.
  - **Start at login with two radio programs** (or one and the modem): they would fight over the radio; warn when a second one is set to start.
  - Smaller: the false "restart needed" marker for pat and rigctld; "Update hamstack" offered when up to date; Updates and Settings blank while loading; the Radios page lacks the station line and notices.
- [ ] **Terminals in the dashboard** (owner asked 2026-11-26, future): two kinds, each with its own warning. (1) A hamstack terminal: a terminal window in the browser that runs only the hamstack command line and its terminal interface (no shell), so the station can be run remotely from the web; normal Settings, with a warning that whoever is logged in to the dashboard can change the station and make it transmit. (2) A full terminal session (a shell as this computer's user) in Expert mode only, with a stronger warning (full control of the computer), asking for the dashboard password again. Today there is one opt-in "Interactive CLI / TUI console" in Remote access (Expert) that gives a full shell; it becomes (2), and (1) is new.
- [ ] **xara** (owner asked 2026-11-25; decided: hamstack runs a xara the person installs, names NR5P/xara as where to get it, and does not download or build it, because the original author withdrew it and the fork has no Linux release or current tags). Built: modem kind, unit, Pat wiring, docs; NR5P main built on the Pi. Next: switch the FT-DX10 to it and try a VARA gateway (owner transmits); `hamstack install xara` with a cargo build type once the fork tags releases; offer Pat's gateway list filtered by service code (`XARA`, `MERCURY`, `DATA2G`) so people see gateways their modem can reach. Screen (view-only, relayed) and `xara-registered` built. Also: refuse a modem switch while Pat is connecting (a switch to xara cut a Pat attempt through Mercury on the Pi; the PTT check alone missed it, and Pat has no clear "connecting" status to read yet).
- [ ] **One page per radio** (owner asked 2026-11-25; step 1 built and seen on the Pi): `/radios` with Radio, Modems, Sound (shared listening, receive-level check), Pat and Ports.
  - Next:
    - (a) A "Radio setup" section: read checks over rigctld from openRMS-Rigs profiles (`~/Projects/openRMS/openRMS-Spec/rigs`, local API :8790, `/dump.json` to bundle), with Fix for the steps a profile allows.
    - (b) Remove the copies elsewhere: the Radio card on Overview becomes a summary linking to the radio's page, modem rows on Services link here, and Settings keeps only station-wide things.
    - (c) The TUI gets the same grouping.
- [ ] **Shared audio layer** (owner asked 2026-11-25; stage 1 built, not yet on a real card).
  - Stage 1 (built): per-rig ALSA dsnoop/dmix/plug config given only to hamstack's services through `ALSA_CONFIG_PATH` (seen working with a null device on the dev laptop). Opt-in `shared_audio`; Mercury, ardopcf and Direwolf use it; `hamstack audio device|test RIG`.
  - Next:
    - (a) Done 2026-11-25 on the Pi (owner OK): shared-audio on for ftdx10, Mercury restarted onto it, and `hamstack audio test ftdx10` recorded twice at once while Mercury ran. Shared audio is left on there. Still to do: an on-air Winlink session through Mercury on the shared device, and ardopcf on it.
    - (b) rigctlWeb's monitor uses `hamstack audio device`.
    - (c) Stage 2: several modems listening at once, with a transmit lock (the one with a session owns PTT; the others report busy). That is the base for a trimode RMS-style gateway.
    - (d) Data2G through the shared device (PortAudio lists devices with a hint).
    - (e) PipeWire machines: use the pipewire ALSA plugin when PipeWire manages the card.
- [ ] **hamgo and integrations — progress 2026-11-15:** steps 2 and 3 done locally in `~/Projects/hamgo` (own git repo, no remote, nothing tagged): module `github.com/ATCUSA/hamgo`, MIT + free-forever README + DONATING, CI file, rules tests (stdlib only, no host imports, no cgo), `safetext`, `rigctl` (rigctlWeb's client and caps parser plus `Abort`, `Caps.FullWatts`, `Caps.PowerRange` from hamstack), `rigctl/rigctltest` (fake rigctld checked against Hamlib 4.7.2's dummy), `docs/STAGED.md` (sources, staging window), `scripts/check-staged.sh`; local workspace file at `~/Projects/.hamgo-work/go.work` (use only with `GOWORK=`). Next: step 4 is the owner gate (create/push `ATCUSA/hamgo`, tag v0.1.0). Audio stays in rigctlWeb until its "move day" (step 9). Original item:
  **hamgo and integrations** (owner decisions 2026-11-15; plan of record: `docs/architecture/hamgo-and-integrations.md`, 18 migration steps, step 0 = this box). Library `github.com/ATCUSA/hamgo`, rigctlWeb is an integration, hamstack audio reaches rigctlWeb parity through hamgo. Stop and ask the owner before: creating/pushing the hamgo repo, tagging, module path changes, merging to main. Open questions are in section 7 of the doc.
- [ ] **Show what needs attention after an update** (built 2026-11-15, verify in the browser): Overview banner from the doctor checks (hide for now; link to Settings, Doctor); repair prints "close and open open programs again". Not done: marking a running app display that started before a fix.
- [ ] **IceWM look (Infadel2, built 2026-11-15):** theme file in ~/.local/share/hamstack/icewm via ICEWM_PRIVCFG, only when the package ships the theme; never touches ~/.icewm. Verify on the Pi, then on Arch/Fedora that Infadel2 ships there.
- [ ] Push the branch and open a PR; tag a release so the plugin validator is installable (then publish the plugin AI-prompt one-liner and website page; ask where the website lives).
- [ ] App folders (`docs/app-folders.md`): waiting on three decisions from the owner (shared logs vs private, seed once vs adopt, convenience of settings for hand-launched apps).
- [ ] Plugin runtime (install, run as user service, proxy at `/plugin/<id>/`, HTTPS for microphone, stop the modem under audio plugins, watchdog that unkeys if a plugin dies). Wait until rigctlWeb has base-path support so a real plugin exists to test.
- [ ] Voice plugin (`~/Projects/hamstack-plugins/voice`): first run on a laptop with fake devices, then the radio's sound card at low power; test in a browser (microphone, AudioWorklet, PTT); Opus later; ID reminder minutes as a setting.
- [ ] rigctlWeb changes for plugin use (list in `docs/plugins/rigctlweb-changes.md`) if the owner wants them done on a branch.
- [ ] Panel ideas from rigctlWeb to consider: VFO A/B and split, RIT/XIT, per-band memory, passband width, tuning step, AGC/preamp/attenuator.
- [ ] Plugin ideas (`docs/plugins/ideas.md`; folders exist under `~/Projects/hamstack-plugins/`): world clock, PSK Reporter map, SHARES/MARS/CAP dashboards, small login (needs clarifying).
- [ ] Re-run the plugin AI-prompt blind tests after the latest wording changes.
- [x] **Audio monitor removed from the v1 release (owner decision 2026-11-15):** the thin Listen button, `/ws/audio` relay and `internal/audiomon` were a cut-down copy of rigctlWeb's monitor. Audio comes in v2 from rigctlWeb's code through hamgo, with its full feature set. The removed work is in git history (commits "Audio monitor: ..."). Pi findings to carry into v2: PipeWire capture works with 2 shared listeners and leaves nothing running; the card is free when idle but PipeWire keeps it a few seconds after a capture, during which direct ALSA (ardopcf with a `plughw:` device) fails busy; `pipewire-alsa` is not installed on the Pi; no `pactl`/`parec` there (ffmpeg listing); the card is found by the node name, not the ffmpeg description. Options for the contention: install `pipewire-alsa`, yield to modems, or listen only when no modem runs.
- [ ] **Shared packages** (superseded by the hamgo item at the top of Planned; first-pass analysis in `docs/modules.md`): rigctld client (3 copies), audiomon (2), plugin manifest. Waiting on the owner: library name, which client API leads, module path rename, and the push go-ahead.

## Done (recent)
- [x] 2026-11-25: **Stuck transmitter on the Pi; transmit watchdog.**
  - What happened: on an ARDOP connect through the shared card, ardopcf hung right after PTT-TRUE. The radio was keyed about 3–4 minutes (around 5 W, 7.100.500). Pat's abort did not release it; stopping ardopcf did, through the unit's unkey step.
  - Cause: the shared playback path (dmix).
  - Fixes:
    - Modems now listen through the shared card but transmit on the card directly.
    - HF modems run supervised (`superviseModem`), with a PTT watchdog through rigctld: keyed more than 30 s means unkey and restart the modem.
    - hamlib's dummy radio runs with `-P RIG`, so keying can be tested.
  - Tests: incus `txguard` (passes on Debian), unit tests for the guard.
  - The dmix hang itself is not understood (ardopcf's TX open after the key-separation fix); playback stays off the shared layer.
- [x] 2026-11-25: **Platform fixes from the Pi session (after deploying v0.2.0-7):**
  - **Update banner nagged about vnc and novnc:** they are distribution packages whose newer upstream release is not packaged yet. They now get the status `newer upstream, not packaged yet` (`updates.NotPackaged`), which is shown with its explanation but not counted. A package that does have a newer version ready still counts. It uses `apt-cache policy` / `pacman -Si` (Fedora: unknown, so it still counts).
  - **Checks at boot said "network is unreachable" for an hour:** an update check that found no network now says so plainly and is repeated after 2 minutes.
  - **Firewall install from the dashboard failed with only "exit status 1":** the dashboard ran it inside itself with `sudo -n`. Firewall, group and browser-component plans now run as jobs (`hamstack plan NAME --yes`), with the password dialog when sudo needs one.
  - **"Permission needed: hamstack system allow" blocked update, restart and shut down:** those already took the password through the dialog. The page no longer stops them, and `system allow` is described as an optional, one-time way to skip the password.
  - **Pat's page:** the recommended setup comes before the settings, open when it has something to apply and folded ("all set") when not.
- [x] 2026-11-25: **More than one HF modem per rig** (owner asked). Built: `alt_modems`, `hamstack rig switch`, Switch buttons on the Radio page, and Services adds an unused modem as one to switch to. With ARDOP alongside a VARA-style modem, ardopcf moves to ARQ+4/+5 and Pat gets both addresses. Tested: unit tests and incus `modemswitch` on Debian. Not tried on the Pi or a real radio. The TUI has no switch yet.
- [x] 2026-11-25: **ardopcf's screen** (owner asked).
  - Off by default, `ardop_gui` per rig, turned on through the owner password.
  - Needs the firewall helper with its port approved. The firewall rule is applied each time ardopcf starts, and without it ardopcf starts with no screen.
  - Shown only through the dashboard at `/ardop/RIG/`: the page and script are fetched from localhost, the `ws://…/ws` line is rewritten to `wss://…/ardop/RIG/ws`, the socket is relayed same-origin only, and it closes with the login.
  - Seen in incus: `--webgui` works, the page binds 0.0.0.0 (as the source says), and the script line matches. The proxy has unit tests with a stand-in. Not seen on the Pi.
- [ ] **Found 2026-11-25:** ardopcf's command and data ports (ARQ+0/+1, or +4/+5) listen on every interface with no password (seen in incus). Like Mercury's, they should default to "this computer only" where the firewall helper is installed. Today their network-access default is "all".
- [x] 2026-11-25: **"1 software update available" that never cleared on a development build.** The Pi runs `dev-944a5b4-…`, whose hamstack row has the status "development build; release available", and the banner and the Updates list counted that as an update. A development build is not behind a release (installing v0.2.0 over it would go back), so it is no longer counted; the note on the hamstack row still says what it is. A build stamped by `task build` (`v0.2.0-6-g…`) compares as up to date.
- [x] 2026-11-25: Follow-ups to the hardening:
  - An open screen keeps its login alive while it is used (keys or mouse within 5 s); one left alone does not.
  - The ardopcf busy-card message says the service gives up after a few tries and how to start it again.
  - The apps scenario's dependency assertion accepts hamlib already being installed.
- [x] 2026-11-25: **Dashboard hardening** (review items; unit tests, some with mutation checks; not yet seen in a browser on the Pi).
  - Logins:
    - Password checks count against the 5-in-10-minutes limit as they start, so parallel guesses are limited. At most two hashes run at once, and old failure records are dropped.
    - The first login after the password is first set is no longer thrown away.
    - The login form is limited to 4 KB, and multipart forms are refused.
  - The dashboard server has an `IdleTimeout`, and its error log keeps everything but TLS handshake noise.
  - `next=` after login refuses control characters, so "/<tab>/evil.example" can't redirect to another site.
  - Open VNC/console relays close within 5 s once their login ends (logout, expiry, password change).
  - Turning on the console or tuning, and changing the password, need the dashboard password again (`ownerOnly`; the page asks).
  - A rig setting can't change the power between a tune's power drop and its "tuning" flag.
- [x] 2026-11-25: **From the Pi logs (ARDOP).** Three fixes, built and tested here, not yet on the Pi.
  - **Pat's ARDOP address:** "ARDOP TNC initialization failed: dial tcp: missing address" means Pat's `ardop.addr` was empty. `pat sync` fills it, but switching a rig's modem never said so. Changing a modem now names the Pat settings that differ and how to apply them (Pat's page, or `hamstack pat sync`). It works from the dashboard (Services, Settings → Rigs) and the TUI.
  - **Busy sound card:** ardopcf stopped at once with "Device or resource busy". It now waits up to 8 s for the card to be free, since a sound server lets go a few seconds after its last client. If the card is still held, the log names the program and pid (`internal/audio/holders.go`). The cause on the Pi is not confirmed yet: run `fuser -v /dev/snd/pcmC2D0*` there while it fails.
  - **"Installed, not in use":** it now offers "Use on RIG instead of MODEM" for a rig that runs another HF modem, instead of only "every rig already has a modem".
  - Not a bug: Pat's "vara initialization failed ... 8310 refused" is Pat's VARA transport pointing at Mercury/Data2G, which do not run while the rig uses ardopcf.
- [x] 2026-11-25: **`system reboot --dry-run` and `system shutdown --dry-run` no longer reboot or power off.** They print the command they would run and stop. The power action is a variable, so the test can never reboot anything.
- [x] 2026-11-25: **Dashboard kept saying updates were available after updating.**
  - Cause: the dashboard keeps program versions in memory for an hour, and updates run in another process (a job, or a terminal), so it never noticed. Running the update again forced a fresh check, which is why the notice cleared then.
  - Fix:
    - The dashboard reads `updates.Fresh()`. It compares a cheap fingerprint of the installed programs' files (path, size, time; `versions.Locate`, nothing run) at most every 5 s, and re-reads versions in the background when it changed.
    - Install, system and self jobs also call `updates.Recheck` when they end.
    - Latest versions still come from the disk cache, so this adds no GitHub requests.
  - Checked: unit tests, including a mutation check. Not seen on the Pi yet.
- [x] 2026-11-25: **sudo password kept away from source builds.**
  - Before: every step of a dashboard job inherited `SUDO_ASKPASS`, and the askpass socket gave the password to anyone who asked, so a source build's `configure` could get root.
  - How the password travels now:
    - The job passes it on a socket pair as fd 3, which the hamstack child reads and closes at start (`install/sudo.go`).
    - Only steps built by `priv` get it, on a socket as input, given only to the system sudo (root-owned setuid in /usr/bin or /bin, never `PATH`'s first).
    - The command runs as `sudo -k -S -p '' -- sh -c 'exec "$@" </dev/null' sh CMD`, so a NOPASSWD rule can't leave the password on the command's input. Real sudo does leave it without the wrapper; the test run showed this.
    - `verifySudoPassword` and `runPrivileged` use the same system sudo and socket.
  - Also:
    - Passwords with line breaks are refused, and `hamstack askpass` is removed.
    - The firewall's sudoers rule goes through `sudoersFileStep` (temp file, `visudo -cf`, `install -m 0440`), like `system allow`.
    - New incus scenario `sudo` (real sudo; password and NOPASSWD accounts; system update, install, source build, dashboard repair, firewall install; fake sudo first on PATH).
    - `incus-test.sh` now removes a stale `~/.local/bin/hamstack` in the test user's home.
  - Results:
    - Debian: everything passes.
    - Fedora and Arch: all password checks pass; ardopcf does not compile (GCC 15, see below); Arch dashboard repair fails (`novnc` is AUR-only).
  - Not checked: the dashboard dialog end to end.
- [ ] **Found 2026-11-25:** all three test containers had a stale root-owned `~/.local/bin/hamstack` (Nov 13) first on the test user's PATH, so incus results from them before today may have tested that old binary. Re-run the full suite on all three distributions before the release.
- [ ] **Found 2026-11-25:** `ardopcf` source build fails on Fedora and Arch (newer GCC: `void f()` declarations rejected, C23 default). Probably needs `-std=gnu17` in its CFLAGS; verify in incus.
- [ ] **Found 2026-11-25:** `hamstack dashboard repair` fails on Arch: `novnc` is not in the official repositories (catalog says distribution package).
- [ ] Plan lines in a job log show the sudo wrapper unquoted (`-p ""` is invisible); `Step.Display` should shell-quote.
- [x] 2026-11-25: `task check` green again: gofmt on `internal/web/server.go`; data race in job stop fixed (the process group id is copied under the lock after Start, and a stop that arrives while a command is starting is acted on); `.station-build/` (Pi cross-build cache, 379 MB, committed by mistake in the previous commit) untracked and ignored. Then the branch was squashed into one commit on top of `origin/main` (no cache, no personal values in history; non-neutral callsigns in README, docs, site and tests replaced). The old history is kept locally only, in branch `backup/pre-squash-2026-11-25`: never push it.
- [x] 2026-11-15: all 37 native `confirm()` boxes replaced by the in-page `askConfirm` dialog (Cancel and Escape say no; tuning dialogs focus Cancel). Checked in a browser: yes/no/Escape. Individual buttons not each clicked.
- [x] Multi-window apps: IceWM verified on the Pi (2026-11-15), the waterfall window is reachable. Doctor "dashboard components" check and `hamstack dashboard repair` (fix button runs as a job with the password dialog) installed it.
- 2026-11-15: logo and favicon; tune message judges the final SWR; `ardopcf` as a modem kind with unkey-on-stop; installed-but-unused programs shown; catalog visibility test; password prompt for jobs and power actions; plugin validator and docs; feedback on every action.
