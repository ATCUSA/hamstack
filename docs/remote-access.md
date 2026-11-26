# Browser dashboard and remote access

The optional HTTPS dashboard shows service status and controls, rigs, sites, stations, devices, programs, settings, network endpoints, checks, logs, GPS and maintenance. It refreshes service status while open. Guided setup offers it; the terminal Settings tab can enable it later.

```sh
hamstack web check
hamstack web on --access local
hamstack web on --desktop --access network
hamstack web on --cert /path/cert.pem --key /path/key.pem
hamstack web password
hamstack web off
```

New setups use a generated self-signed certificate unless a certificate and key are supplied. Browsers will ask you to decide whether to trust a self-signed certificate. A dashboard password is required and stored as a hash. Existing dashboards keep their previous access setting; inspect it before changing it. Certificate files at configured paths can be replaced on renewal. Automatic ACME provisioning is a future feature.

### Logins

- **Wrong passwords:** after five wrong passwords from one address in ten minutes, the dashboard stops checking passwords from that address for five minutes. Passwords sent at the same time count too, and at most two passwords are checked at once.
- **Ending a login:** logging out, a login expiring (12 hours without use) or changing the password also closes any app or desktop open in the browser through that login, within a few seconds.
- **Owner-only changes:** these ask for the dashboard password again, even when you are logged in:
  - turning on the browser console (a terminal as this computer's user);
  - turning on tuning from the dashboard (it keys the transmitter);
  - turning on Expert mode, ardopcf's screen, xara's registered speeds or Mercury's page on the network;
  - setting Data2G's page password;
  - changing the dashboard password.

  Turning any of them off does not ask.

### The administrator (sudo) password

Some dashboard actions need root: installing or updating programs, a system update, repairing the dashboard's packages, reboot and shut down. The dashboard shows why and which commands would run as root, then asks for your account's password for that one action. It checks it with a single `sudo -k` attempt. After three wrong passwords it stops asking for 15 minutes, so that it cannot get your account locked.

The password is kept in memory only while the action runs. It is never written to a log, a file, a command line or an environment variable.

- **Reboot and shut down:** the dashboard runs the one `sudo` command itself, with the password on that command's input.
- **Installs, updates and repairs:** these run as a job. The hamstack program that carries out the job receives the password on a private socket, which it reads and closes before it starts anything. It then gives the password only to the system's own `sudo` (the root-owned, setuid one in `/usr/bin` or `/bin`, never a `sudo` found earlier on `PATH`). It does this for each step that runs as root, and the password goes on that step's input. The command run as root gets an empty input, so a `NOPASSWD` rule that never reads the password cannot pass it on to the command. The other steps of a source build run code downloaded from upstream. They get no socket, file, helper program or environment value that leads to the password.

What this does not protect against:

- Another program running as your user that can read hamstack's memory. Linux allows this between programs of the same user when `kernel.yama.ptrace_scope` is 0.
- A program that replaces a hamstack binary you can write to, such as `~/.local/bin/hamstack`, before the next job starts it.

What has been tested: real `sudo` in incus containers (`scripts/incus-test.sh --only sudo`). The tests cover:

- an account that needs its password and one with a `NOPASSWD` rule;
- right and wrong passwords;
- system update, a package install, a source build and dashboard repair, with the password passed the way a job passes it;
- a fake `sudo` placed first on `PATH`.

All of it passes on Debian. On Fedora and Arch every password check passes, but two things fail for reasons that have nothing to do with the password: the `ardopcf` source build (newer GCC rejects its code) and, on Arch, dashboard repair (`novnc` is not in Arch's official repositories). The dashboard's password dialog itself has only been tested against a fake `sudo`.

## Apps and the whole desktop

TigerVNC gives each supported app its own virtual display. noVNC displays it in the browser through the authenticated HTTPS dashboard. Choose an installed app on the dashboard or use `hamstack app open wsjtx --browser`. A native VNC viewer can instead use an SSH tunnel to the localhost port shown by `hamstack app open wsjtx`. VNC servers themselves remain on loopback.

The optional whole-desktop share uses x0vncserver on X11 or wayvnc with a compatible Wayland compositor. `hamstack web check` reports the detected requirements. wayvnc does not support GNOME, KDE/Plasma or Weston; an unknown compositor is reported as unverified. A compatible session and its capture and input protocols must be running for desktop sharing to work. Individual virtual-display apps do not require an existing desktop session.

## Settings levels

Safe features are on by default, and every one can be turned off in Settings. Settings has three levels:

- **the normal list:** everyday switches, among them each radio's xara screen and monitor, and Data2G's page through
  the dashboard;
- **Advanced** (folded): what changes what goes on the air or who can reach the station: shared listening on a
  radio's sound card, ardopcf's screen (whose buttons can make the radio transmit);
- **Expert mode** (a switch; turning it on asks for the dashboard password): xara's registered VARA speeds, Mercury's
  page on the network with no password, the browser console and the dashboard's Tune button.

Expert settings can always be turned off. Turning one on needs Expert mode, and the server refuses it otherwise
(hiding the button is not the protection). Each one also asks for the dashboard password again. The terminal
interface shows every setting, marked `[advanced]` or `[expert]`, since it runs as the computer's own user.

## Advanced console

Remote access includes an optional browser console. It is off by default, needs Expert mode to turn on, and needs
xterm, TigerVNC and noVNC. Enabling it gives every signed-in dashboard user full terminal access as the operating-system user running hamstack. It starts the TUI; quitting the TUI leaves a shell. Use it only when all dashboard users are trusted with that access. You can enable or disable it in Remote access or with `hamstack web console on|off`.

Web package operations display a plan, and noninteractive sudo reports when local authorization is needed. The console can handle operations that need an interactive terminal. Restarting the dashboard briefly disconnects browser sessions; disabling it requires local access to enable it again.

See [Network access](network.md) for listener and firewall controls. Tests cover the dashboard's handlers and a simulated browser-page smoke test; these do not establish that every app, compositor, radio or VNC viewer works on every machine.
