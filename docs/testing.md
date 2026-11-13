# Testing

    task check          # what CI runs: gofmt, vet, unit tests (race detector), build, script and privacy checks
    task incus-test     # the real-systemd scenarios in containers (below)

    go test ./...                              # unit tests, a few seconds

    scripts/incus-test.sh                      # the real thing: Debian and Fedora under real systemd
    scripts/incus-test.sh --distro arch        # also: arch (debian, fedora, arch)
    scripts/incus-test.sh --only multirig,sites,direwolf
    scripts/incus-test.sh --fresh              # start from brand new instances
    scripts/incus-test.sh --cleanup            # delete the test instances

`incus-test.sh` needs [incus](https://linuxcontainers.org/incus/). It builds hamstack, creates one
system container per distribution (named `hamstack-test-<distro>`, and it never touches any other
instance), and runs `scripts/incus-scenarios.sh` inside each as an unprivileged user with `sudo`, the way a
real user would. The distributions run in parallel, each with a full log, and you get a summary.

The scenarios (about 190 assertions per distribution) cover: installing the programs; the guided setup;
several rigs at once (their own ports, stopping, disabling, removing); sites and switching between them;
migrating a config and units from the single-radio versions; Direwolf with a CAT radio and with a Digirig
that is not plugged in; doctor (including the old-hamlib check); the group and linger handling across a
new login and a restarted service manager; `pat sync`; a GPS (simulated receiver, a real gpsd); packet listen and call against a real Direwolf; the kernel AX.25 stack (only where the kernel has it: run it in a VM); a reboot (services must come back by themselves);
and the interface driven through tmux. The first run downloads images and builds Mercury, so it takes
several minutes; later runs reuse the instances and take about six.
