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

### The administrator (sudo) password

Some dashboard actions (installing or updating programs, a system update, repairing the dashboard's packages, reboot and shut down) need root. The dashboard shows why and which commands would run as root, then asks for your account's password for that one action. It is checked with a single `sudo -k` attempt, kept only in memory while the action runs, and never written to a log, a file, a command line or an environment variable. The program that carries out the action receives it on a private socket, which it reads and closes before it starts anything. From then on it gives the password only to the `sudo` of each step that runs as root, on that step's input. The steps of a source build, which run code downloaded from upstream, have no way to reach it. After three wrong passwords the dashboard stops asking for 15 minutes, so that it cannot lock your account. What this does not protect against: another program running as your user that can read hamstack's memory, which Linux allows when `kernel.yama.ptrace_scope` is 0.

Tested with real `sudo` on Debian in an incus container (password and NOPASSWD accounts, right and wrong passwords). The dashboard's password dialog itself has only been tested against a fake `sudo`.

## Apps and the whole desktop

TigerVNC gives each supported app its own virtual display. noVNC displays it in the browser through the authenticated HTTPS dashboard. Choose an installed app on the dashboard or use `hamstack app open wsjtx --browser`. A native VNC viewer can instead use an SSH tunnel to the localhost port shown by `hamstack app open wsjtx`. VNC servers themselves remain on loopback.

The optional whole-desktop share uses x0vncserver on X11 or wayvnc with a compatible Wayland compositor. `hamstack web check` reports the detected requirements. wayvnc does not support GNOME, KDE/Plasma or Weston; an unknown compositor is reported as unverified. A compatible session and its capture and input protocols must be running for desktop sharing to work. Individual virtual-display apps do not require an existing desktop session.

## Advanced console

Remote access includes an optional browser console. It is off by default and needs xterm, TigerVNC and noVNC. Enabling it gives every signed-in dashboard user full terminal access as the operating-system user running hamstack. It starts the TUI; quitting the TUI leaves a shell. Use it only when all dashboard users are trusted with that access. You can enable or disable it in Remote access or with `hamstack web console on|off`.

Web package operations display a plan, and noninteractive sudo reports when local authorization is needed. The console can handle operations that need an interactive terminal. Restarting the dashboard briefly disconnects browser sessions; disabling it requires local access to enable it again.

See [Network access](network.md) for listener and firewall controls. Tests cover the dashboard's handlers and a simulated browser-page smoke test; these do not establish that every app, compositor, radio or VNC viewer works on every machine.
