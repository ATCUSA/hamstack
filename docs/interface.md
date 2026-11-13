# The interface

Run `hamstack` with no arguments in a terminal. Everything you can do on the command line you can do here:

| Tab | What you do there |
|---|---|
| Services | start, stop, restart, enable at login, read logs, install the services (`U`) |
| Rigs | add a radio (pick the model, name it, choose a modem; its USB devices are detected), switch it on or off, change its modem, remove it, identify it by unplug and replug |
| Sites | save the current setup as a site, switch sites, set a site's grid, jump to the site whose radios are plugged in |
| Stations | add a callsign, import your existing Pat configs, switch station, remove |
| Devices | live list of USB serial ports and sound cards; one that appears while the interface is open is marked NEW; assign it to a rig |
| Programs | installed versus latest upstream; install, update, or build from source |
| Settings | Pat on/off, Mercury web UI, grid square, start at boot, serial and sound access |
| Doctor | every check, and `f` runs the selected check's own fix |
| Logs | the journal of one service; `[` and `]` change the service (or `l` on a service) |
| Network | every port the stack uses and who can reach it (see "Network") |

Keys `1` to `9` open the first nine tabs and `0` the tenth (Network); Tab and the arrow keys move between them.

Small operations happen inside it with prompts. Anything that needs a terminal or a password (installs,
`sudo`, the guided setup `w`, `rig identify`, a doctor fix) is handed the same terminal: you see exactly what
it prints, press Enter, and the interface comes back and refreshes. Before one of those runs, the prompt shows
the exact command, and nothing runs without a `y`. On a fresh install the first screen offers the guided setup.
