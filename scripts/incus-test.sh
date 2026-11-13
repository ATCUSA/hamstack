#!/usr/bin/env bash
# Tests hamstack on real distributions under real systemd, using incus system containers.
#
#   scripts/incus-test.sh                          Debian and Fedora, every scenario
#   scripts/incus-test.sh --distro debian          one distribution
#   scripts/incus-test.sh --only multirig,sites    some scenarios (see --list)
#   scripts/incus-test.sh --fresh                  start from brand new instances
#   scripts/incus-test.sh --cleanup                delete the test instances and stop
#
# It only ever touches instances whose names start with "hamstack-test-" (override with --prefix, which
# must still be something you are happy for it to delete). The first run downloads images and builds
# Mercury inside each instance, so it takes several minutes; later runs reuse the instances.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
PREFIX="hamstack-test"
DISTROS="debian,fedora"
ONLY=""
FRESH=0; CLEANUP=0; BINARY=""; KEEP=1; LIST=0

ALL_SCENARIOS="install setup multirig sites migrate direwolf doctor groups linger patsync gps ax25 network apps tui reboot"

usage() { sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "incus-test: $*" >&2; exit 2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --distro)  DISTROS="${2:?}"; shift ;;
    --only)    ONLY="${2:?}"; shift ;;
    --fresh)   FRESH=1 ;;
    --cleanup) CLEANUP=1 ;;
    --binary)  BINARY="${2:?}"; shift ;;
    --prefix)  PREFIX="${2:?}"; shift ;;
    --list)    LIST=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (try --help)" ;;
  esac
  shift
done

if [ "$LIST" = 1 ]; then echo "distros: debian fedora arch"; echo "scenarios: $ALL_SCENARIOS"; exit 0; fi
command -v incus >/dev/null || die "incus is not installed"
incus version >/dev/null 2>&1 || die "cannot reach the incus server (is it running, and are you in the incus-admin group?)"
case "$PREFIX" in ""|*[!a-zA-Z0-9-]*) die "the prefix must be letters, digits and dashes";; esac

image_for() {
  case "$1" in
    debian) echo "${HAMSTACK_IMAGE_DEBIAN:-images:debian/13}" ;;
    fedora) echo "${HAMSTACK_IMAGE_FEDORA:-images:fedora/45}" ;;
    arch)   echo "${HAMSTACK_IMAGE_ARCH:-images:archlinux}" ;;
    *) return 1 ;;
  esac
}
name_for() { echo "$PREFIX-$1"; }

# --- cleanup -------------------------------------------------------------------------------------------
if [ "$CLEANUP" = 1 ]; then
  found=0
  for n in $(incus list -f csv -c n 2>/dev/null); do
    case "$n" in "$PREFIX"-*) echo "deleting $n"; incus delete -f "$n"; found=1 ;; esac
  done
  [ "$found" = 0 ] && echo "no instances named $PREFIX-* to delete"
  exit 0
fi

# --- what to run ---------------------------------------------------------------------------------------
IFS=',' read -ra DISTRO_LIST <<<"$DISTROS"
for d in "${DISTRO_LIST[@]}"; do image_for "$d" >/dev/null || die "unknown distribution $d (debian, fedora, arch)"; done
if [ -n "$ONLY" ]; then IFS=',' read -ra SCEN <<<"$ONLY"; else read -ra SCEN <<<"$ALL_SCENARIOS"; fi
for s in "${SCEN[@]}"; do case " $ALL_SCENARIOS " in *" $s "*) ;; *) die "unknown scenario $s (see --list)";; esac; done
DO_REBOOT=0; RUN=()
for s in "${SCEN[@]}"; do if [ "$s" = reboot ]; then DO_REBOOT=1; else RUN+=("$s"); fi; done

OUT="$(mktemp -d /tmp/hamstack-incus.XXXXXX)"

# --- the binary ----------------------------------------------------------------------------------------
if [ -z "$BINARY" ]; then
  GO="$(command -v go || true)"
  [ -z "$GO" ] && [ -x "$HOME/.local/share/mise/shims/go" ] && GO="$HOME/.local/share/mise/shims/go"
  [ -n "$GO" ] || die "Go is not installed; build the binary yourself and pass --binary PATH"
  echo "building hamstack (static)..."
  (cd "$ROOT" && CGO_ENABLED=0 "$GO" build -o "$OUT/hamstack" ./cmd/hamstack) || die "build failed"
  BINARY="$OUT/hamstack"
fi
[ -x "$BINARY" ] || die "no executable at $BINARY"

# --- one distribution ----------------------------------------------------------------------------------
inst_exec()  { local n="$1"; shift; incus exec "$n" -- "$@"; }
as_tester()  { local n="$1"; shift; incus exec "$n" -- sudo -u tester -H "$@"; }

wait_ready() {  # the instance has systemd up and a working network
  local n="$1" i
  for i in $(seq 1 60); do inst_exec "$n" systemctl is-system-running 2>/dev/null | grep -qE 'running|degraded' && break; sleep 2; done
  for i in $(seq 1 60); do inst_exec "$n" getent hosts github.com >/dev/null 2>&1 && return 0; sleep 2; done
  return 1
}

prepare() {  # packages, the unprivileged user, lingering
  local n="$1" d="$2"
  case "$d" in
    debian) inst_exec "$n" sh -c 'apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo ca-certificates curl iproute2 python3 >/dev/null 2>&1' ;;
    fedora) inst_exec "$n" sh -c 'dnf install -y -q sudo ca-certificates iproute python3 >/dev/null 2>&1' ;;
    arch)   inst_exec "$n" sh -c 'pacman -Sy --noconfirm --needed sudo ca-certificates iproute2 python >/dev/null 2>&1' ;;
  esac
  inst_exec "$n" sh -c 'id tester >/dev/null 2>&1 || useradd -m -s /bin/bash tester
    echo "tester ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/tester; chmod 440 /etc/sudoers.d/tester
    loginctl enable-linger tester'
  local i
  for i in $(seq 1 30); do inst_exec "$n" systemctl is-active user@"$(inst_exec "$n" id -u tester)".service >/dev/null 2>&1 && return 0; sleep 1; done
  return 1
}

push_files() {
  local n="$1"
  incus file push "$BINARY" "$n/usr/local/bin/hamstack" --mode 0755 >/dev/null
  incus file push "$HERE/incus-scenarios.sh" "$n/home/tester/scenarios.sh" >/dev/null
  inst_exec "$n" chown tester /home/tester/scenarios.sh
}

run_distro() {
  local d="$1" n; n="$(name_for "$d")"
  local log="$OUT/$d.log"
  {
    echo "### $d ($n)"
    if [ "$FRESH" = 1 ] && incus info "$n" >/dev/null 2>&1; then echo "removing the old instance"; incus delete -f "$n"; fi
    if ! incus info "$n" >/dev/null 2>&1; then
      echo "creating $n from $(image_for "$d")"
      incus launch "$(image_for "$d")" "$n" || { echo "FAIL: could not create the instance"; exit 1; }
    else
      incus start "$n" >/dev/null 2>&1
    fi
    wait_ready "$n" || { echo "FAIL: the instance did not come up with a working network"; exit 1; }
    prepare "$n" "$d" || { echo "FAIL: could not prepare the instance"; exit 1; }
    push_files "$n"
    if [ ${#RUN[@]} -gt 0 ]; then as_tester "$n" bash /home/tester/scenarios.sh "${RUN[@]}"; fi
    if [ "$DO_REBOOT" = 1 ]; then
      echo "=== reboot"
      as_tester "$n" bash /home/tester/scenarios.sh reboot-prepare
      echo "restarting the instance"
      incus restart "$n" || echo "FAIL: restart failed"
      wait_ready "$n" || echo "FAIL: the instance did not come back"
      sleep 5
      as_tester "$n" bash /home/tester/scenarios.sh reboot-check
    fi
  } >"$log" 2>&1
}

echo "logs in $OUT"
pids=()
for d in "${DISTRO_LIST[@]}"; do run_distro "$d" & pids+=($!); done
echo "running ${SCEN[*]} on ${DISTRO_LIST[*]} (in parallel)..."
for p in "${pids[@]}"; do wait "$p"; done

# --- the summary ---------------------------------------------------------------------------------------
rc=0
echo
for d in "${DISTRO_LIST[@]}"; do
  log="$OUT/$d.log"
  pass=$(grep -c '^PASS:' "$log"); fail=$(grep -c '^FAIL:' "$log")
  printf '%-8s %3d passed, %d failed\n' "$d" "$pass" "$fail"
  grep '^SCENARIO' "$log" | sed -E 's/^SCENARIO ([^ ]+) pass=([0-9]+) fail=([0-9]+) seconds=([0-9]+)/    \1: \2 passed, \3 failed (\4s)/'
  if [ "$fail" -gt 0 ]; then rc=1; grep '^FAIL:' "$log" | sed 's/^/    /'; fi
  if ! grep -q '^TOTAL\|^SCENARIO' "$log"; then echo "    no results: see $log"; rc=1; fi
done
echo
[ "$rc" = 0 ] && echo "ALL PASSED" || echo "FAILURES (full logs: $OUT)"
exit "$rc"
