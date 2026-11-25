# Apps in their own folders (design, not built)

**Goal.** hamstack-managed radio programs keep their settings in a folder hamstack owns, so (1) the person's original
configuration is never touched, (2) uninstalling hamstack leaves the programs working exactly as before, and (3)
hamstack's changes are always a reviewable, revertible set. Today hamstack edits the originals in place (a backed-up,
ledger-tracked edit: `app-config` entries in `~/.local/share/hamstack/changes.json`). That is safe but not isolated.

## The hard question: data must not fork

Settings are cheap to separate. **Logs, mailboxes, contacts and message stores are not**: if hamstack runs WSJT-X
with a private folder, QSOs logged there are missing from the person's own WSJT-X log and the reverse. A private
Pat mailbox would hide the person's real Winlink messages. So each app's files fall into two groups:

- **Profile** (radio, audio, ports, modem addresses): hamstack-owned copy, seeded from the original, safe to throw away.
- **Data** (logs, mail, contacts, adif, templates): **shared with the original**, never copied, never deleted.

## Per app: mechanism and how it was checked

| App | Separation mechanism | Status |
|---|---|---|
| WSJT-X, WSJT-X Improved | `--rig-name NAME` selects a separate ini and a separate data folder name | Scratch-`HOME` run proved config isolation; whether log/ADIF stay shared under `--rig-name` is **unverified** |
| JS8Call | Scratch `HOME` (same Qt layout); no profile switch known | Scratch-`HOME` proven for config; sharing of `ALL.TXT`/log needs a symlink design, **unverified** |
| fldigi | `--home-dir DIR` (it appends `.fldigi`) | Option exists; contacts/logbook live in that home, so they would **fork**: needs symlinks for logs |
| flrig | `--config-dir DIR` | Settings only; nothing to fork |
| Pat | `--config FILE` (already how hamstack runs it: a `hamstack-ID.json`, moved to `~/.config/pat/` on uninstall) | Mailbox lives under Pat's own data dir, referenced by the config's `mycall`: **shared**, unchanged |
| Xastir | `-c`/config dir is HOME-relative | Not investigated |
| Direwolf, ardopcf, Mercury, Data2G | Already configured by hamstack-generated files in hamstack's own folder | No originals to protect |

"Proven" means: ran the app with a scratch `HOME` in a test and saw it read and write only there. Nothing in the table
claims the data-sharing half is solved; that is the work.

## Proposed layout

    ~/.local/share/hamstack/apps/<app>/profile/    hamstack's copy of the settings (seeded from the original once)
    ~/.local/share/hamstack/apps/<app>/launch      the exact command line / environment hamstack uses
    <original data locations>                       untouched; shared by symlink or by the app's own setting

## Uninstall

`hamstack uninstall` already undoes recorded system changes and never deletes app data. For app folders it will ask,
per app, with the sizes shown: **merge** hamstack's profile back over the original (backing the original up first),
**keep** the folder (the app keeps working from the launcher that uninstall leaves), or **discard** it (settings only;
data was never in there). Nothing is deleted without that answer.

## Open decisions for the owner

1. For fldigi and JS8Call: symlink logs/contacts into the private folder (works everywhere, slightly fragile), or keep them in the original folder and only isolate the settings file?
2. Seed once from the original and never sync back, or offer "adopt hamstack's profile" as an explicit action?
3. Is "originals never modified" worth losing the convenience that a program opened by hand (not through hamstack) has the hamstack radio settings? (Today it does; with folders it will not.)
