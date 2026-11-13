# Contributing to hamstack

Thank you for helping. hamstack is a small tool with a big surface: it touches radios, sound cards, serial ports,
package managers and systemd. A little care goes a long way, and the checks below keep it safe for the next person.

## Ways to help (you do not need to write Go)

- **Test with real hardware** and send a [hardware report](https://github.com/ATCUSA/hamstack/issues/new?template=hardware_report.yml).
  This is the most valuable contribution: almost everything is tested in containers, very little on a real radio.
  See [docs/hardware-testing.md](docs/hardware-testing.md).
- **Report bugs** with `hamstack version` and `hamstack doctor` output (redact anything private).
- **Add a radio profile**: a small JSON file ([how](#add-a-radio-profile)).
- **Add a program**: a catalog entry ([how](#add-a-program)).
- Improve the docs, or fix a typo. The files in `docs/` are also the website's documentation section, so one edit fixes both.

## Getting set up

You need Go (the version in `mise.toml`/`go.mod`), `git`, and [Task](https://taskfile.dev) (`mise use task`, your package manager, or
`go install github.com/go-task/task/v3/cmd/task@latest`); Task runs the tasks in `Taskfile.yml`, which are plain shell and Go commands you can also run by hand. For the real-systemd tests you also need
[incus](https://linuxcontainers.org/incus/).

```sh
git clone https://github.com/ATCUSA/hamstack && cd hamstack
task                # list every task
task check         # what CI runs: format, vet, unit tests (race detector), build, script and privacy checks
task build         # ./bin/hamstack
```

Run a development build without touching your real setup by pointing it at throwaway directories:

```sh
export HOME=$(mktemp -d) XDG_CONFIG_HOME=$HOME/.config   # then run ./bin/hamstack ... --dry-run
```

`install`, `setup`, `units install`, `start` and friends change the machine they run on (they install packages and
write systemd units). **Develop and test those inside the incus containers**, not on your own computer:
`task incus-test` builds the binary, creates Debian, Fedora (add Arch with `task incus-test -- --distro debian,fedora,arch`) containers, and runs the
scenarios in `scripts/incus-scenarios.sh` under real systemd as an unprivileged user.

## The rules that matter

1. **No personal data.** Examples, tests and docs use neutral values only: callsigns `N0CALL`, `K0TEST`, `K0EOC`,
   `K1ABC`; grid squares `FN31pr` and `EM10dg`; home directory `/home/u`; email `example.com`. Never a real person's.
   `task privacy-check` enforces the generic part; put your own callsign, grid and name in a git-ignored
   `.privacy-deny` file (one regular expression per line) and it guards you too. Enable it for every commit:
   `ln -sf ../../scripts/privacy-check.sh .git/hooks/pre-commit` (it checks what is about to be committed).
2. **Never store or read a Winlink password.** It lives in the station's Pat config. hamstack only touches the
   few Pat settings it documents, with a backup.
3. **Show before you run.** Anything that installs software, uses `sudo` or writes outside `~/.config`, `~/.local`
   and the user's systemd units must show the exact command first and ask. Source builds show a disclaimer.
4. **Never execute a downloaded file on your own machine.** Builds happen in containers.
5. **Tests with the change.** A bug fix gets a test that fails without it. New behaviour gets a unit test, and an
   assertion in `scripts/incus-scenarios.sh` if it touches systemd, packages or the network.
6. **Be honest in the docs.** If something is only tested in a container, say so. No claims about hardware you did not use.

## Add a radio profile

A profile is a JSON file in `internal/profile/profiles/` (see `ic7300.json`): an `id`, a `name`, the hamlib model number
(`rigctl -l`) and the CAT baud rate, optional USB ids and audio-card hints. If you tested it on the real radio, say
so in `notes` and send a hardware report; if you did not, say that instead. `go test ./internal/profile` checks it parses.

## Add a program

A program is an entry in `internal/catalog/catalog.go`: its name, the binary and the flag that prints its version (or
`NoVersionFlag`), where its newest release is published (GitHub or SourceForge), the distribution package names, what
it requires, suggests and conflicts with, and (if it can be built) a source recipe with its build dependencies.
`internal/catalog/catalog_test.go` checks the entry is complete and that every relation points at a real program.
Then build it from source on at least Debian, Fedora and Arch:

```sh
scripts/incus-test.sh --distro debian,fedora,arch --only apps     # then try:  hamstack install NAME --from-source
```

## Pull requests

- Keep them focused; one idea per PR. Link the issue.
- Fill in the template; it is the checklist above.
- Describe what you ran. "Tested on Fedora 45 in incus, scenarios apps and sites" is perfect.
- Commit messages: a short summary line, then why. We do not require a particular format.
- By contributing you agree your work is released under the [MIT license](LICENSE).

## Using AI coding agents

Welcome, with the same rules as any contributor: you are responsible for what you submit, and it must pass the checks.
[AGENTS.md](AGENTS.md) holds the project rules in the form agents read (Claude Code, Codex, Cursor, Copilot and others).
If an agent helped materially you may say so in the PR; we do not require it.
