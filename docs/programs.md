# Programs and updates

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
A source build shows what it will install and where it downloads from first. Programs that cannot print their version without a display (the Qt
applications) have it read from the package database, or from a record hamstack keeps for its own builds.

Every source recipe was built to completion on Debian 13, Fedora 45 and Arch (the fl suite, Xastir, JS8Call, ardopcf, WSJT-X Improved); the
official WSJT-X is built on Debian and Fedora only (Arch no longer ships a Qt 5 module it needs: use WSJT-X Improved there). The built programs were
checked to install and report a version, not run: the Qt programs need a display, and nothing was used on the air.

**WSJT-X's decoder needs an executable stack.** Its Fortran code builds small pieces of code on the stack and runs
them, so `jt9` only works when its stack is marked executable. WSJT-X Improved 3.2.1 built from source on a
Raspberry Pi 5 (Debian 13, GCC 14) was not marked so. WSJT-X ran and showed the waterfall, then failed with
"Subprocess error ... jt9 ... Process crashed" on the first signal it had to decode. Debian's own package marks
`jt9`, and only `jt9`, executable-stack. So hamstack now marks `jt9` after building WSJT-X or WSJT-X Improved.
For a build made before this, `hamstack doctor` says so, and `hamstack app repair` marks it without a rebuild.
On the Pi, the marked `jt9` decoded a test signal and then live FT8 in WSJT-X. Not yet checked: whether an x86-64
build has the same fault.

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

## Suggestions, releases and maintenance

Suggestions and requirements identify components already installed. Choosing all suggested programs adds only the missing ones. The catalog also includes TigerVNC (`vnc`), noVNC (`novnc`) and `wayvnc`; see [Browser dashboard and remote access](remote-access.md) before choosing whole-desktop sharing.

The TUI and browser dashboard check installed programs and hamstack on startup and poll hourly. `hamstack updates` displays the shared inventory; `hamstack updates self --dry-run` reviews the latest hamstack release. Self-update requires a matching platform asset and a published SHA-256 digest or checksum entry, and retains the previous executable. Development builds warn that a published release may not contain unpublished changes. Distribution-managed programs remain managed by the package manager.

`hamstack maintenance` reviews pending service restarts, user-manager reloads, stale group sessions and operating-system reboot markers. Restarting can interrupt an active radio session. Reboot requires an explicit warning and confirmation, including when an installation used `--yes`.
