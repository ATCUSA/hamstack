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
    hamstack rig set hf modem none         # also: name, profile, bands, cat, audio
    hamstack pat on|off                    # Winlink client, shared by all rigs

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

    hamstack station add club --callsign N0CLUB --name "Club"   # starter Pat config created if missing
    hamstack station import-pat                                 # adopt existing ~/.config/pat/*.json as stations
    hamstack stations                                           # list
    hamstack use club                                           # switch: regenerates config, restarts mercury + pat

Radios (profiles) and stations are independent: any station works with any radio.
hamstack never stores your Winlink password; it lives in the station's Pat config.
Only one station is active (and can use the radio) at a time. Pat logs go to
`~/.local/state/hamstack/`.
