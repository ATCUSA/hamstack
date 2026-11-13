# Copilot instructions for hamstack

hamstack is a Go tool that installs and runs amateur-radio software (Pat, Mercury, Direwolf, hamlib, WSJT-X, fldigi...) as
systemd user units. Full rules are in [AGENTS.md](../AGENTS.md); the essentials:

- `task check` must pass (gofmt, `go vet`, `go test -race`, build, script and privacy checks). Add a test with every change.
- Use only neutral example data: callsigns N0CALL/K0TEST/K0EOC/K1ABC, grids FN31pr/EM10dg, home `/home/u`, email example.com.
- Never read or store a Winlink password. Show the exact command and ask before installing, using sudo, or writing outside
  `~/.config`, `~/.local` and the user's systemd units (build install `Step`s, do not `exec` directly).
- Do not run host-changing commands (install, setup, units, start, sudo, systemctl) on the developer's machine: use the incus tests.
- Never execute downloaded files on the host. Text from the air, gpsd or USB goes through `internal/safetext` before printing.
- Error messages are read by radio operators: say what happened and what to do next.
