# Plugin ideas (not started)

Ideas the owner has mentioned. Nothing here is a commitment or has a design yet. Each plugin lives in its own repo
(manifest points at one `owner/repo` for releases), under `~/Projects/hamstack-plugins/<name>/` while in development.

| Idea | What it might be | Notes |
|---|---|---|
| **Voice** (in progress) | Push-to-talk voice from the browser to the radio | `~/Projects/hamstack-plugins/voice`, its own repo |
| **SHARES / MARS / CAP dashboards** | Dashboards and settings that help operators in those programs with their processes: net schedules and frequencies, message formats, check-in logs | Confirmed by the owner: these are government/military/volunteer programs (SHARES, MARS, Civil Air Patrol). Still to ask: which processes in each matter most before designing. Probably forms and procedure checklists plus per-program frequency/schedule presets |
| **Small login-compatible service** | A small, lightweight login for something "eight-if compatible" | The note is unclear (heard as "eight-if"): need the owner to say what it must be compatible with before anything is designed |
| **PSK Reporter map** | Map of where your signals were heard (and who you hear) from PSK Reporter | Needs the network; use PSK Reporter's public data within its terms and rate limits |
| **World clock / HamClock-style map** | Time zones, UTC clock, grey line, day/night map in the style of HamClock | Pure display, no radio access: the simplest first plugin to test the runtime with |

Suggested order when the runtime exists: world clock (no radio, tests the proxy and base path), PSK Reporter map (network only),
then the ones that touch the radio. Questions for the owner are marked above.
