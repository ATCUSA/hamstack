# Packet radio: Direwolf, AGW and AX.25

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

## Watching and calling on packet (no kernel needed)
With Direwolf running, hamstack can watch the air and make a connected-mode call itself, over Direwolf's AGW port:

    hamstack packet listen              # every packet Direwolf hears, time-stamped (like axlisten)
    hamstack packet call K0ABC          # connect and chat; "~." on a line or Ctrl+C disconnects (like axcall)
    hamstack packet call K0ABC WIDE1-1,WIDE2-1   # through digipeaters
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
    hamstack ax25 call K0ABC            # axcall through it        hamstack ax25 listen   # axlisten
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
