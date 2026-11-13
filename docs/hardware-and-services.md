# Hardware, permissions and services

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

## Services
systemd user units: `hamstack-rigctld@RIG`, `hamstack-mercury@RIG`, `hamstack-direwolf@RIG`, `hamstack-pat` and, with a GPS, `hamstack-gpsd`:
- mercury `BindsTo` rigctld (stopping rigctld stops it) and waits for rigctld's port before starting;
- pat `Wants` mercury;
- the pat unit follows the active station (its config file and web UI address);
- a missing radio makes rigctld exit once with a clear message (no restart loop);
- the CAT serial device is resolved when the service starts, so replugging is fine.
Mercury uses ARQ port 8300, the same as VARA HF: do not run both.
