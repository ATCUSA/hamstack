# Testing with real hardware

hamstack is tested thoroughly in containers and a VM, but a container has no radio. If you have hardware, a report of what
worked and what did not is the most useful contribution you can make. File it with the
[hardware report template](https://github.com/ATCUSA/hamstack/issues/new?template=hardware_report.yml).

Do **not** put your callsign, grid square or email in a report you want kept private; N0CALL and FN31pr are fine stand-ins.

## Safe first steps (no transmitting)

```sh
hamstack devices            # does it see your radio's serial port and sound card, and group them?
hamstack doctor             # what does it say about the radio, permissions, ports?
hamstack rig add hf --profile <your profile> --modem none
hamstack start hf           # rigctld only: starts rig control, does not transmit
rigctl -m 2 -r localhost:4532 f      # read the frequency through hamstack's rigctld
```

If your radio is not in the short list, search: in the guided setup choose `s`, or `hamstack rig add` with a profile you made
from hamlib's list. Tell us the baud rate and anything you had to change.

## What to try, in order

| Area | What to check | Notes |
|---|---|---|
| CAT | `rigctl ... f` reads the frequency; changing it on the radio changes the reading | needs the right CAT baud rate on the radio and in the profile |
| Audio | `hamstack devices` shows the codec; `doctor` finds the sound card | note the ALSA card name |
| PTT | key the radio from `rigctl ... T 1` then `T 0` (into a dummy load!) | |
| Mercury / Direwolf | the service starts and stays up; the web UI or `hamstack packet listen` shows activity | transmit only into a dummy load or on a frequency you are licensed for |
| GPS | `hamstack gps use`, `hamstack gps`, `hamstack gps grid` | cold start can take minutes, with a view of the sky |
| Digirig | a Direwolf rig with `--profile digirig`; the PTT line is the serial port's RTS | profile ids and sound-card names are unverified |
| AX.25 kernel stack | `hamstack ax25 setup`, `attach`, `call` | needs a kernel with AX.25 (Debian, Fedora; not recent mainline kernels) |

## Transmitting

hamstack never transmits by itself. Anything on the air is your responsibility under your licence: use a dummy load for
tests, identify properly, and follow the rules of the band.
