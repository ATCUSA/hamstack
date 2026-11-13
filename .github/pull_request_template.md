## What and why

<!-- What does this change, and what problem does it solve? Link the issue: Fixes #123 -->

## How it was checked

- [ ] `task check` passes (gofmt, vet, unit tests with the race detector, build, script and privacy checks)
- [ ] New behaviour has a test (unit test, or an assertion in `scripts/incus-scenarios.sh`)
- [ ] If it changes what runs under systemd or installs software: `task incus-test` on at least one distribution (say which)
- [ ] If it touches real hardware (a radio, GPS, Digirig): what you tried it with, and what happened

## Safety and privacy

- [ ] No real callsigns, grid squares, emails, serial numbers or paths in code, tests or docs (use N0CALL, K0TEST, FN31pr, `/home/u`)
- [ ] Nothing new runs as root, or writes outside `~/.local`, `~/.config`, `~/.local/state` and the user's systemd units, without showing the command first and asking
- [ ] No downloaded file is executed on the developer's own machine (builds happen in containers)

## Docs

- [ ] README or `docs/` updated if behaviour or commands changed
- [ ] If a program or radio was added: its recipe is in the catalog or `profiles/`, with a test
