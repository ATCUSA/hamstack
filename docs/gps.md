# GPS (gpsd)

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
