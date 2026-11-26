# Radios, sites and stations

## Rigs: one or several radios
A **rig** is one radio you own and how it is wired up: its model (profile), the serial port and sound
card it uses, an optional modem, its bands, and its own ports. Several can run at once, for example
an HF rig with Mercury and a VHF/UHF rig.

    hamstack rig add hf   --profile ic7300mk2 --modem mercury --bands hf    # detects its USB devices if plugged in
    hamstack rig add vhf  --profile ftdx10 --bands vhf
    hamstack rigs                          # what you have, and whether each radio is plugged in
    hamstack rig identify vhf              # unplug / replug to find (or re-find) its USB devices
    hamstack rig disable vhf               # keep it in the config but do not run it
    hamstack rig set hf modem none         # also: name, profile, alt, bands, cat, audio
    hamstack pat on|off                    # Winlink client, shared by all rigs

### The Radios page

The dashboard's **Radios** page puts everything about one radio in one place, with a tab per radio:

- **Radio:** its controls: frequency, mode, power, meters, Tune.
- **Modems:** each modem it runs or can switch to. Each has Open web UI or Open screen, Start/Stop/Log, and Use
  instead for one on standby. **Also set up** adds an installed modem to switch to.
- **Sound:** the sound card, shared listening on or off, and **Check receive level**. That records 3 seconds of
  the radio's audio (through the shared card, so the modem keeps running), compares it with about −25 dBFS, and
  says what to change. On the command line: `hamstack audio level RIG`.
- **Pat:** whether Pat's settings match this radio's modems and ports, with Apply.
- **Ports:** who can reach each of its ports.

Seen on a Raspberry Pi with an FT-DX10: the page, and a level check reading −24.2 dBFS while Mercury ran. The
radio-setup checks (AGC and similar, from openRMS-Rigs profiles) are not built yet.

### More than one HF modem on a rig

A rig can have ARDOP (ardopcf) and a VARA-style modem (Mercury or Data2G) set up together, but it runs one at a
time. Each modem opens the radio's sound card for itself, and the radio has one transmitter.

    hamstack rig set hf alt ardopcf        # hf keeps its modem and can also switch to ardopcf ("none" clears)
    hamstack rig switch hf ardopcf         # stops the modem it runs (and stops it starting at login), starts ardopcf

The Radio page has a **Switch to …** button for each one. On the Services page, an installed modem that no rig
uses can be added to a rig this way. Switching is refused while the radio is transmitting.

With both kinds on a rig, ardopcf listens on ports of its own in the rig's block (ARQ port + 4 and + 5). That way
`hamstack pat sync` points Pat's ARDOP and VARA connections at both, and either works after a switch. A rig with
ardopcf alone keeps the ports it always had.

Tested in an incus container (Debian, real systemd): adding, switching both ways, Pat's addresses. Switching on a
real radio has not been tried.

### Shared sound card (experimental)

A modem opens the radio's sound card for itself. A second program, such as another modem or something listening
to the radio, then gets "Device or resource busy". With shared audio on, hamstack gives the rig's modems a shared
view of the card instead:

    hamstack rig set hf shared-audio on    # then restart the rig's modem
    hamstack audio test hf                 # records twice at once from the shared card; plays nothing
    hamstack audio device hf               # the device name and the one setting another program needs to use it

This uses ALSA's own sharing, with no sound server and no root:

- **dsnoop** lets several programs read the receive audio;
- **dmix** mixes what they play;
- a **plug** device converts to each program's sample rate.

hamstack writes the configuration per rig into its state folder (`~/.local/state/hamstack/alsa/RIG.conf`). It
gives it only to the programs it starts, through `ALSA_CONFIG_PATH`, so the system's ALSA setup, `~/.asoundrc` and
the desktop's sound are not changed. The card's channel count and sample rate come from
`/proc/asound/CARD/stream0`; a CM108 interface such as the Digirig captures in mono.

Used by: Mercury, ardopcf and Direwolf. Not yet by Data2G, which picks its card from its own device list.

Only listening is shared. A modem transmits on the card itself, not through the mixing layer: ardopcf once hung
there with the radio keyed. Only one program may transmit anyway.

Limits:

- It cannot share a card that a sound server (PipeWire, PulseAudio) has open at the same time.
- Until hamstack arbitrates who may transmit, a rig still runs one modem at a time (`hamstack rig switch`).

Tested on a Raspberry Pi with a Yaesu FT-DX10's built-in USB CODEC (stereo, 48 kHz):

- Mercury ran on the shared device;
- `hamstack audio test` recorded with two more programs at the same time;
- Mercury stayed up.

ardopcf also ran on it, with two more programs recording at the same time. ardopcf is strict about periods: it
wants exactly 10 ms for playback, and about 600 frames for capture, set before the rate. So the shared card runs
capture in 50 ms periods and playback in 10 ms periods; at any sample rate those convert to whole frames.

Not yet tested: a real connection through it, any other interface, and Direwolf on it.

### Transmit watchdog

While a rig's HF modem (ardopcf, Mercury, Data2G) runs, hamstack watches the radio's PTT through rigctld every
second. If the radio stays keyed for more than 30 seconds, hamstack unkeys it and restarts the modem; the modem's
log says why. These modems send bursts of a few seconds and then listen, so a key held that long means the modem
has hung. This happened once on a test station: ardopcf hung with the radio keyed for several minutes, and Pat's
abort could not release it. Programs that key the radio themselves, such as WSJT-X and Fldigi, stop the HF modem
first, so the watchdog never cuts their transmissions.

Tested in an incus container (Debian, real systemd) with hamlib's dummy radio keyed by hand: unkeyed after 30 s,
and the modem was restarted.

### Programs that reach the radio through PipeWire

WSJT-X Improved's Qt 6 build and JS8Call reach the sound card through PipeWire (or PulseAudio), not ALSA. PipeWire's
session manager, WirePlumber, works out which directions of a card it can offer when it first sees the card, by
opening each one. If a modem has the card's playback open at that moment (the modems transmit straight to the card),
PipeWire offers the card for listening only, and keeps doing so until WirePlumber starts again. WSJT-X then finds
no output. It quietly falls back to "TCI audio" for both input and output, and hears nothing.

So when one of these programs starts from hamstack, hamstack first stops the HF modem, then checks with `pw-dump`
what PipeWire offers of each rig's card. If a direction is missing and nothing holds it any more, hamstack restarts
WirePlumber (a user service) so PipeWire looks again, and says so in the program's log
(`hamstack logs vnc@wsjtx`). Restarting WirePlumber does not stop programs that are already recording.

Seen on a Raspberry Pi 5 with a Yaesu FT-DX10's USB codec: before the fix, WSJT-X Improved 3.2 had no audio. After
restarting WirePlumber by hand, PipeWire offered the card's output again and WSJT-X received.
The automatic restart is covered by unit tests. It has not yet been seen to work on its own when a program starts.

### ardopcf's screen

ardopcf has a web page of its own: spectrum, waterfall, constellation, receive levels, busy and PTT indicators,
and a log. It also has a drive-level slider and Two-tone and ID buttons. Those make ardopcf send audio to the
radio, and the radio transmits if it keys on audio (VOX or DATA VOX). The page has no password and listens on
every network interface (checked with ardopcf 1.0.4.1.3 in an incus container). So hamstack:

- keeps it **off** until you turn it on per rig: `hamstack rig set hf ardop-gui on`, or **Turn on ARDOP screen** on
  the Radio page, which asks for the dashboard password again;
- only allows that once hamstack's firewall helper is installed and has approved the page's port. Installing the
  firewall helper again approves a port added since;
- each time ardopcf starts, applies the firewall rule that keeps the port to this computer first. If that fails,
  ardopcf starts without the page and its log says why;
- shows the page only through the dashboard (**Open ARDOP screen** on the Radio page, `/ardop/RIG/`), behind its
  login, refusing other sites. The connection closes when the login ends.

Its port is the one after ardopcf's data port (ARQ port + 2, or + 6 on a rig that also has a VARA-style modem).
The proxy is tested against a stand-in for ardopcf's page. The real page has been seen to start and serve its
script; it has not been watched on a real radio.

`setup winlink` creates the first rig for you. Systemd units are templates (`hamstack-rigctld@hf`,
`hamstack-mercury@hf`) so adding a rig never needs new unit files, and they hold no device paths: each
runs `hamstack run`, which finds the radio's serial port and sound card when the service starts, using the
USB identity (vendor, product, serial number) saved with the rig. A radio on another USB port, or moved to
another site, keeps working.

Service names are `rigctld@hf`, `mercury@hf` and `pat`. Commands take a service (`mercury@hf`), a kind
(`mercury`: every one), a rig (`hf`: everything of that rig) or `all`.

Each rig gets its own ports: rig control counts up from 4532, and a rig's Mercury uses a block of ten from
8300 (its web UI, broadcast and message history are separate too). Ports are kept when a rig is removed so
the others never move under Pat's config. Pat has one HF modem setting, so with several Mercury rigs Pat's
is wired to the first; `doctor` checks that it points at a Mercury that exists. Configs from before rigs
existed are migrated automatically.

## Choosing a radio
The guided setup (and the Rigs tab) shows a short list first: the dummy radio (testing without hardware), a Digirig, eight common rigs (IC-7300,
IC-7300 MK2, IC-705, IC-7610, FT-991A, FT-710, FT-DX10) and FLRig (control through flrig). Anything else is one search away: `s`, then type part of
a name (`kx3`, `ic 7100`, `yaesu`). The list is every radio the *installed* hamlib supports (it offers to install hamlib first), matched
loosely (spaces and dashes ignored, letters in order), paged. A radio without a profile gets one made and saved on the spot, asking the serial speed
(it shows the range hamlib says the model supports). Profiles other than the dummy, Digirig, IC-7300 MK2 and FT-DX10 were written from hamlib's list and
typical settings, not tested with the radio: check the CAT rate on the radio matches.

With a radio that is not plugged in, setup finishes and says which services will wait; they show **no radio** (not a failure) and start as soon as it is
plugged in and you run `hamstack start`. A Digirig with Mercury switches that rig to Direwolf (it has no CAT control to key Mercury with) and says so.

## Sites: home, the EOC, the club
A site is a place you operate from. It records which of your rigs are there, which station (callsign) is
active, and the grid square, so moving between places is one command instead of a dozen.

    hamstack site add home                  # save what is set up now (rigs, station, grid) as "home"
    hamstack site add eoc --rigs vhf,dig --station eoc --grid FN31pr
    hamstack site use eoc                   # its rigs on, the others off, its station and grid; services restarted
    hamstack sites                          # your places, and how many of each one's radios are plugged in now
    hamstack site here                      # which site do the plugged-in radios match?  (--switch to go there)
    hamstack pat sync                       # wire Pat's config to the rigs and grid you are at now

The current site is a live view: enabling or adding a rig, or switching station, is recorded in it, and
`config set grid` changes the current site's grid (or the default when you have no sites). `site use`
restarts things only if the stack was running, and stops the rigs that are not part of the new site.

hamstack suggests a site but never switches by itself: `doctor` and `sites` say when the radios plugged in
belong to a different site. Radios are recognised by the USB identity saved with each rig, so a rig with no
saved device (the dummy radio) takes no part in the matching.

Moving to a new site changes the grid and the radios, but Pat's own config is never rewritten behind your
back. `doctor` warns when Pat's locator, HF modem or packet TNC no longer match, and `hamstack pat sync`
fixes exactly those settings (the locator, the rig connections, the VARA HF address, the AGWPE address),
keeping everything else, passwords included, and leaving a `.hamstack-bak` copy.

## Stations (several callsigns)
A *station* is an operating identity: a callsign with its own Pat config, mailbox and
web UI port, for example a personal call, a club call and an emergency-communications call.
Stations are yours to define; none are built in, and they are kept in `~/.config/hamstack/`.

    hamstack station add club --callsign K0CLUB --name "Club"   # starter Pat config created if missing
    hamstack station import-pat                                 # adopt existing ~/.config/pat/*.json as stations
    hamstack stations                                           # list
    hamstack use club                                           # switch: regenerates config, restarts mercury + pat

Radios (profiles) and stations are independent: any station works with any radio.
hamstack never stores your Winlink password; it lives in the station's Pat config.
Only one station is active (and can use the radio) at a time. Pat logs go to
`~/.local/state/hamstack/`.
