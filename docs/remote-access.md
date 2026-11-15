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

## Apps and the whole desktop

TigerVNC gives each supported app its own virtual display. noVNC displays it in the browser through the authenticated HTTPS dashboard. Choose an installed app on the dashboard or use `hamstack app open wsjtx --browser`. A native VNC viewer can instead use an SSH tunnel to the localhost port shown by `hamstack app open wsjtx`. VNC servers themselves remain on loopback.

The optional whole-desktop share uses x0vncserver on X11 or wayvnc with a compatible Wayland compositor. `hamstack web check` reports the detected requirements. wayvnc does not support GNOME, KDE/Plasma or Weston; an unknown compositor is reported as unverified. A compatible session and its capture and input protocols must be running for desktop sharing to work. Individual virtual-display apps do not require an existing desktop session.

## Advanced console

Remote access includes an optional browser console. It is off by default and needs xterm, TigerVNC and noVNC. Enabling it gives every signed-in dashboard user full terminal access as the operating-system user running hamstack. It starts the TUI; quitting the TUI leaves a shell. Use it only when all dashboard users are trusted with that access. You can enable or disable it in Remote access or with `hamstack web console on|off`.

Web package operations display a plan, and noninteractive sudo reports when local authorization is needed. The console can handle operations that need an interactive terminal. Restarting the dashboard briefly disconnects browser sessions; disabling it requires local access to enable it again.

See [Network access](network.md) for listener and firewall controls. Tests cover the dashboard's handlers and a simulated browser-page smoke test; these do not establish that every app, compositor, radio or VNC viewer works on every machine.
