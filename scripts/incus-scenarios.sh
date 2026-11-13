#!/usr/bin/env bash
# Runs inside a test instance, as the unprivileged user "tester", and checks that hamstack really works on
# this distribution under real systemd. Started by scripts/incus-test.sh; usable by hand too:
#   bash incus-scenarios.sh [scenario ...]        (no arguments: every scenario)
# Each assertion prints PASS: or FAIL:, and each scenario ends with a SCENARIO summary line.

export XDG_RUNTIME_DIR="/run/user/$(id -u)"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
export PATH="$HOME/.local/bin:/usr/local/bin:$PATH"
export TERM="${TERM:-xterm-256color}"

PASS=0; FAILS=0
ok()   { echo "PASS: $*"; PASS=$((PASS + 1)); }
bad()  { echo "FAIL: $*"; FAILS=$((FAILS + 1)); }

# check DESCRIPTION COMMAND...: passes when the command succeeds.
check() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi; }
# fails DESCRIPTION COMMAND...: passes when the command fails.
fails() { local d="$1"; shift; if "$@" >/dev/null 2>&1; then bad "$d (it succeeded)"; else ok "$d"; fi; }
# expect_out DESCRIPTION REGEX COMMAND...: passes when the output matches the regex.
expect_out() {
  local d="$1" re="$2"; shift 2
  local out; out=$("$@" 2>&1)
  if grep -Eq -- "$re" <<<"$out"; then ok "$d"; else bad "$d (wanted /$re/, got: $(head -c 300 <<<"$out" | tr '\n' '|'))"; fi
}
# expect_no DESCRIPTION REGEX COMMAND...: passes when the output does NOT match the regex.
expect_no() {
  local d="$1" re="$2"; shift 2
  local out; out=$("$@" 2>&1)
  if grep -Eq -- "$re" <<<"$out"; then bad "$d (found /$re/: $(grep -E -- "$re" <<<"$out" | head -2 | tr '\n' '|'))"; else ok "$d"; fi
}
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', wanted '$3')"; fi; }

port_open()   { hamstack wait-port "$1" "${2:-6}" >/dev/null 2>&1; }
port_closed() { ! hamstack wait-port "$1" 1 >/dev/null 2>&1; }
unit_of()     { case "$1" in *@*) echo "hamstack-${1%@*}@${1#*@}.service" ;; *) echo "hamstack-$1.service" ;; esac; }
active()      { [ "$(systemctl --user is-active "$(unit_of "$1")" 2>/dev/null)" = active ]; }
wait_active() { local i; for i in $(seq 1 "${2:-20}"); do active "$1" && return 0; sleep 1; done; return 1; }
hamlib_minor() { rigctld --version 2>/dev/null | sed -n 's/.*Hamlib \([0-9]*\.[0-9]*\).*/\1/p' | head -1; }

# reset puts hamstack back to nothing: no units, no config, no state.
reset() {
  hamstack units remove >/dev/null 2>&1
  rm -rf "$HOME/.config/hamstack" "$HOME/.local/state/hamstack"
  rm -f "$HOME/.config/systemd/user/hamstack-"*.service
  systemctl --user daemon-reload 2>/dev/null
  systemctl --user reset-failed 2>/dev/null
  return 0
}

# need_programs makes sure the three programs are installed (the install scenario does it properly).
need_programs() {
  if command -v rigctld >/dev/null && command -v pat >/dev/null && command -v mercury >/dev/null; then return 0; fi
  hamstack install hamlib pat mercury --yes >/tmp/hamstack-install.log 2>&1
}

# ---------------------------------------------------------------------------------------------------------

sc_install() {
  hamstack install hamlib pat mercury --yes >/tmp/hamstack-install.log 2>&1
  eq "install exits 0" "$?" 0
  check "rigctld is on PATH" command -v rigctld
  check "pat is on PATH" command -v pat
  check "mercury is on PATH" command -v mercury
  expect_out "a second install has nothing to do" "Nothing to install" hamstack install hamlib pat mercury --yes
  expect_no "versions reads every program's version" "version unknown" hamstack versions --offline
  expect_out "versions shows pat" "^pat +[0-9]" hamstack versions --offline
}

sc_setup() {
  need_programs; reset
  hamstack setup winlink --components rigctld,mercury,pat --radio dummy --callsign K0TEST --ui off --yes --skip-groups --skip-install </dev/null >/tmp/hamstack-setup.log 2>&1
  eq "guided setup exits 0" "$?" 0
  for s in rigctld@dummy mercury@dummy pat; do wait_active "$s" || true; check "$s is running" active "$s"; done
  for p in 4532 8300 8080; do check "port $p is open" port_open "$p"; done
  expect_out "the station was created" "k0test" hamstack stations
  hamstack doctor >/tmp/hamstack-doctor.log 2>&1
  eq "doctor finds no failure on a fresh setup" "$?" 0
}

sc_multirig() {
  need_programs; reset
  hamstack rig add hf --profile dummy --modem mercury --bands hf >/dev/null 2>&1
  hamstack rig add vhf --profile dummy --modem mercury --bands vhf >/dev/null 2>&1
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1
  hamstack start >/dev/null 2>&1; sleep 4
  for s in rigctld@hf mercury@hf rigctld@vhf mercury@vhf pat; do check "$s is running" active "$s"; done
  for p in 4532 4533 8300 8310; do check "port $p is open" port_open "$p"; done
  eq "each rig's Mercury has its own ports" "$(grep -h '^arq_tcp_base_port' ~/.config/hamstack/rigs/*/mercury.ini | sort | tr '\n' ' ')" "arq_tcp_base_port = 8300 arq_tcp_base_port = 8310 "
  eq "and its own message history" "$(grep -h '^path' ~/.config/hamstack/rigs/*/mercury.ini | sort -u | wc -l)" 2
  hamstack stop vhf >/dev/null 2>&1; sleep 1
  check "stopping one rig leaves the other running" active mercury@hf
  fails "the stopped rig's services are down" active mercury@vhf
  hamstack rig disable vhf >/dev/null 2>&1
  eq "a disabled rig has no unit instances left running" "$(systemctl --user list-units 'hamstack-*@vhf.service' --no-legend | wc -l)" 0
  hamstack rig enable vhf >/dev/null 2>&1; hamstack start >/dev/null 2>&1; sleep 3
  check "an enabled rig starts again" active mercury@vhf
  hamstack rig set hf modem none >/dev/null 2>&1; sleep 1
  fails "removing a rig's modem stops it" active mercury@hf
  check "the other rig's modem is untouched" active mercury@vhf
  hamstack rig remove vhf >/dev/null 2>&1
  expect_no "removing the last Mercury rig removes its template" "mercury@" bash -c "ls ~/.config/systemd/user | grep hamstack"
  check "rig control for the remaining rig still runs" active rigctld@hf
}

sc_sites() {
  need_programs; reset
  hamstack config set grid EM10dg >/dev/null 2>&1
  hamstack rig add hf --profile dummy --modem mercury >/dev/null 2>&1
  hamstack rig add eoc --profile dummy --modem mercury >/dev/null 2>&1
  hamstack station add me --callsign K0HOM >/dev/null 2>&1
  hamstack station add eoc --callsign K0EOC --addr localhost:8081 >/dev/null 2>&1
  hamstack site add home --rigs hf --station me --grid EM10dg >/dev/null 2>&1
  hamstack site add eoc --rigs eoc --station eoc --grid FN31pr >/dev/null 2>&1
  hamstack site use home >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1; hamstack start >/dev/null 2>&1; sleep 4
  check "at home: the home rig runs" active mercury@hf
  fails "at home: the EOC rig is off" active mercury@eoc
  hamstack site use eoc >/dev/null 2>&1; sleep 4
  fails "at the EOC: the home rig has stopped" active mercury@hf
  check "at the EOC: the EOC rig runs" active mercury@eoc
  check "at the EOC: Pat runs with the EOC station" port_open 8081
  expect_out "at the EOC: Mercury has the EOC callsign" "callsign = K0EOC" cat ~/.config/hamstack/rigs/eoc/mercury.ini
  expect_out "the header reports the site" "site: +eoc" hamstack config
  hamstack site use home >/dev/null 2>&1; sleep 4
  check "back home: the home rig runs again" active mercury@hf
  expect_out "back home: Mercury has the home callsign" "callsign = K0HOM" cat ~/.config/hamstack/rigs/hf/mercury.ini
  hamstack site use eoc >/dev/null 2>&1; hamstack stop >/dev/null 2>&1; hamstack site use home >/dev/null 2>&1; sleep 1
  eq "switching with nothing running starts nothing" "$(systemctl --user list-units 'hamstack-*' --state=active --no-legend | wc -l)" 0
}

sc_migrate() {
  need_programs; reset
  mkdir -p ~/.config/hamstack/pat ~/.config/systemd/user
  echo '{"mycall":"K0TEST","http_addr":"localhost:8080"}' > ~/.config/hamstack/pat/me.json
  # a config and units exactly as the single-radio versions of hamstack wrote them
  cat > ~/.config/hamstack/config.json <<EOF
{"profile":"dummy","ui":false,"active":"me","grid":"EM10dg","stations":[{"id":"me","callsign":"K0TEST","pat_config":"$HOME/.config/hamstack/pat/me.json","http_addr":"localhost:8080"}]}
EOF
  for u in rigctld mercury pat; do printf '[Unit]\nDescription=old single-radio unit\n[Service]\nExecStart=/bin/sleep 3600\n' > ~/.config/systemd/user/hamstack-$u.service; done
  systemctl --user daemon-reload; systemctl --user start hamstack-rigctld.service hamstack-mercury.service
  expect_out "the old config is read as one rig with Mercury" "dummy: dummy, modem mercury" hamstack config
  expect_out "and Pat stays on" "pat: +on" hamstack config
  expect_out "and the grid is kept" "grid: +EM10dg" hamstack config
  hamstack units install >/dev/null 2>&1
  fails "the old fixed rigctld unit is gone" test -e ~/.config/systemd/user/hamstack-rigctld.service
  fails "the old fixed mercury unit is gone" test -e ~/.config/systemd/user/hamstack-mercury.service
  check "the rigctld template is installed" test -e ~/.config/systemd/user/hamstack-rigctld@.service
  check "the mercury template is installed" test -e ~/.config/systemd/user/hamstack-mercury@.service
  eq "the old services were stopped" "$(pgrep -fc 'sleep 3600')" 0
  hamstack start >/dev/null 2>&1; sleep 4
  for s in rigctld@dummy mercury@dummy pat; do check "$s runs after migration" active "$s"; done
  hamstack config set grid EM10dg >/dev/null 2>&1     # any change saves the config, which rewrites it in the new form
  expect_out "once saved, the config is in the new form" '"rigs"' cat ~/.config/hamstack/config.json
  # a rig has its own "profile" (indented), so look only at the top level for the old settings
  expect_no "and no longer holds the old top-level settings" '^  "(components|profile)"' cat ~/.config/hamstack/config.json
}

sc_direwolf() {
  need_programs
  hamstack install direwolf --yes >/tmp/hamstack-direwolf.log 2>&1
  eq "direwolf installs" "$?" 0
  check "direwolf runs" bash -c 'direwolf -h 2>&1 | grep -q "Dire Wolf"'
  expect_out "its version is read" "^direwolf +[0-9]" hamstack versions --offline
  reset
  hamstack rig add pkt --profile dummy --modem direwolf >/dev/null 2>&1
  hamstack rig add pkt2 --profile dummy --modem direwolf >/dev/null 2>&1
  hamstack rig add dig --profile digirig --modem direwolf >/dev/null 2>&1
  hamstack station add me --callsign K0PKT >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1
  hamstack start >/dev/null 2>&1; sleep 5
  check "direwolf runs for a rig keyed through rigctld" active direwolf@pkt
  check "and for a second one" active direwolf@pkt2
  for p in 8000 8001 8002 8003; do check "direwolf port $p is open" port_open "$p"; done
  if command -v ss >/dev/null; then
    eq "the two direwolf processes listen on exactly their own four ports" "$(ss -ltnp 2>/dev/null | grep direwolf | awk '{print $4}' | sed 's/.*://' | sort -n | tr '\n' ' ')" "8000 8001 8002 8003 "
  fi
  expect_out "the generated config keys the radio through rigctld" "PTT RIG 2 localhost:4532" cat ~/.config/hamstack/rigs/pkt/direwolf.conf
  expect_out "and removes the default KISS port" "KISSPORT 0" cat ~/.config/hamstack/rigs/pkt/direwolf.conf
  sleep 2
  eq "a Digirig rig with nothing plugged in fails once" "$(systemctl --user show hamstack-direwolf@dig.service -p ExecMainStatus --value)" 3
  eq "and does not restart in a loop" "$(systemctl --user show hamstack-direwolf@dig.service -p NRestarts --value)" 0
  hamstack station add pktstn --callsign K0PKS --addr localhost:8099 >/dev/null 2>&1
  expect_out "a new station's Pat config uses the first direwolf's AGWPE port" '"addr": "localhost:8000"' cat ~/.config/hamstack/pat/pktstn.json
  expect_out "doctor sees the packet TNC" "pkt: packet TNC port +port 8000 is in use by the hamstack direwolf@pkt" hamstack doctor
  # rigctld does not exit promptly while a client (Direwolf, Mercury) is attached, so stopping things in the
  # wrong order costs systemd's 90 second timeout. Removing everything must take seconds.
  local t0=$SECONDS; hamstack units remove >/dev/null 2>&1; local took=$((SECONDS - t0))
  if [ "$took" -lt 25 ]; then ok "removing the services is quick (${took}s)"; else bad "removing the services took ${took}s: something was stopped in the wrong order"; fi
}

sc_doctor() {
  need_programs; reset
  hamstack rig add main --profile dummy --modem mercury >/dev/null 2>&1
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1; hamstack start >/dev/null 2>&1; sleep 4
  hamstack doctor >/tmp/hamstack-doctor.log 2>&1
  eq "a healthy setup passes doctor" "$?" 0
  expect_no "and shows no failure" "\[FAIL\]" cat /tmp/hamstack-doctor.log
  # An IC-7300 MK2 needs hamlib 4.7 or newer; doctor must say so on an older one, and not otherwise.
  hamstack rig add icom --profile ic7300mk2 --no-detect >/dev/null 2>&1
  case "$(hamlib_minor)" in
    4.[0-6]) expect_out "an old hamlib is reported as unable to drive the IC-7300 MK2" "icom: radio supported by hamlib +hamlib 4\.[0-6][^ ]* has no driver" hamstack doctor ;;
    *)       expect_out "a current hamlib supports the IC-7300 MK2" "icom: radio supported by hamlib +Icom IC-7300 MK2" hamstack doctor ;;
  esac
  expect_out "a radio that is not plugged in is a warning, not a failure" "icom: radio \(CAT port\)" hamstack doctor
  hamstack rig remove icom >/dev/null 2>&1
}

sc_groups() {
  reset
  local serial; serial=$(hamstack groups 2>/dev/null | awk 'NR==2{print $1}')
  [ -n "$serial" ] || { bad "could not tell which group owns serial ports"; return; }
  local uid; uid=$(id -u)
  # Every check below goes through a brand new login (a new process), because this script's own process keeps
  # whatever groups it started with. A rerun must not depend on how an earlier run left things.
  local fresh="sudo -u $USER -H env XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS=$DBUS_SESSION_BUS_ADDRESS PATH=$PATH"
  baseline() {
    sudo gpasswd -d "$USER" "$serial" >/dev/null 2>&1; sudo gpasswd -d "$USER" audio >/dev/null 2>&1
    sudo systemctl restart "user@$uid.service"; sleep 4          # the service manager forgets the groups too
  }
  baseline
  $fresh hamstack linger on >/dev/null 2>&1                      # services run with nobody logged in, so audio matters too
  $fresh hamstack rig add icom --profile ic7300mk2 --no-detect >/dev/null 2>&1   # a radio with a serial port, so doctor checks access to it
  expect_out "not in the $serial group: groups says so" "^$serial .* no +no +no" $fresh hamstack groups
  expect_out "and offers the fix" "Fix: hamstack groups fix" $fresh hamstack groups
  # A login that started before the fix, and asks afterwards:
  ( $fresh bash -c 'sleep 8; hamstack groups' >/tmp/groups-late.txt 2>&1 ) & local probe=$!
  sleep 2
  $fresh hamstack groups fix --yes >/tmp/hamstack-groups.log 2>&1
  eq "groups fix exits 0" "$?" 0
  wait "$probe"
  expect_out "now a member, but a login that began before it does not have it" "^$serial .*yes +no" cat /tmp/groups-late.txt
  expect_out "and it is told to log in again" "log out and back in" cat /tmp/groups-late.txt
  expect_out "a new login has it, but the service manager does not yet" "^$serial .*yes +yes +no" $fresh hamstack groups
  sudo systemctl restart "user@$uid.service"; sleep 4
  expect_out "after the user manager restarts the services have it too" "^$serial .*yes +yes +yes" $fresh hamstack groups
  expect_out "and doctor agrees" "serial port permission +you are in the \"$serial\" group and your services have it" $fresh hamstack doctor
  baseline                                                       # leave things as we found them
}

sc_linger() {
  need_programs; reset
  hamstack setup winlink --components rigctld,mercury,pat --radio dummy --callsign K0TEST --ui off --yes --skip-groups --skip-install </dev/null >/dev/null 2>&1
  hamstack linger off >/dev/null 2>&1
  expect_out "linger off is reported" "linger is off" hamstack linger
  expect_out "doctor warns that services start at login, not boot" "start at boot +services start when you log in" hamstack doctor
  hamstack linger on >/dev/null 2>&1
  expect_out "linger on is reported" "linger is on" hamstack linger
  expect_out "doctor is satisfied" "start at boot +services start at boot" hamstack doctor
}

sc_patsync() {
  need_programs; reset
  hamstack config set grid EM10dg >/dev/null 2>&1
  hamstack rig add hf --profile dummy --modem mercury >/dev/null 2>&1
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack rig add eoc --profile dummy --modem mercury >/dev/null 2>&1
  hamstack site add home --rigs hf --grid EM10dg >/dev/null 2>&1
  hamstack site add eoc --rigs eoc --grid FN31pr >/dev/null 2>&1
  hamstack site use eoc >/dev/null 2>&1
  python3 - <<EOF
import json, os
p = os.path.expanduser("~/.config/hamstack/pat/me.json")
d = json.load(open(p)); d["secure_login_password"] = "sekret-do-not-touch"; d["listen"] = ["telnet"]
json.dump(d, open(p, "w"), indent=2)
EOF
  cp ~/.config/hamstack/pat/me.json /tmp/me-before.json
  expect_out "doctor notices Pat's locator is for the old place" "Pat locator +Pat's locator is EM10dg but your grid here is FN31pr" hamstack doctor
  expect_out "and that Pat's HF modem points at the old rig" "Pat HF modem +Pat's VARA HF address uses port 8300" hamstack doctor
  hamstack pat sync >/dev/null 2>&1
  eq "pat sync exits 0" "$?" 0
  expect_out "the locator moved" '"locator": "FN31pr"' cat ~/.config/hamstack/pat/me.json
  expect_out "the HF modem moved to the EOC rig's Mercury" '"addr": "localhost:8310"' cat ~/.config/hamstack/pat/me.json
  expect_out "the password was not touched" "sekret-do-not-touch" cat ~/.config/hamstack/pat/me.json
  expect_out "unrelated settings were kept" '"telnet"' cat ~/.config/hamstack/pat/me.json
  eq "a backup of the previous file was kept, byte for byte" "$(cmp -s /tmp/me-before.json ~/.config/hamstack/pat/me.json.hamstack-bak && echo same)" same
  expect_out "doctor is now satisfied about the locator" "Pat locator +Pat's locator \(FN31pr\) matches" hamstack doctor
  expect_out "a second sync changes nothing" "already matches" hamstack pat sync
}

sc_reboot_prepare() {
  need_programs; reset
  hamstack setup winlink --components rigctld,mercury,pat --radio dummy --callsign K0TEST --ui off --yes --skip-groups --skip-install </dev/null >/dev/null 2>&1
  hamstack linger on >/dev/null 2>&1
  wait_active pat || true
  check "services are running and enabled before the reboot" active mercury@dummy
  eq "and enabled at login" "$(systemctl --user is-enabled hamstack-mercury@dummy.service)" enabled
}

sc_reboot_check() {
  local s
  for s in rigctld@dummy mercury@dummy pat; do wait_active "$s" 60 || true; check "$s came back by itself after the reboot" active "$s"; done
  for p in 4532 8300; do check "port $p is open after the reboot" port_open "$p"; done
}

sc_tui() {
  need_programs; reset
  if ! command -v tmux >/dev/null; then
    if command -v apt-get >/dev/null; then sudo apt-get install -y tmux >/dev/null 2>&1
    elif command -v dnf >/dev/null; then sudo dnf install -y tmux >/dev/null 2>&1
    elif command -v pacman >/dev/null; then sudo pacman -S --noconfirm tmux >/dev/null 2>&1; fi
  fi
  command -v tmux >/dev/null || { bad "tmux is not available, so the interface cannot be driven"; return; }
  local t=hstest
  cap() { tmux capture-pane -t "$t" -p 2>/dev/null; }
  snd() { tmux send-keys -t "$t" "$@"; sleep 0.8; }
  tmux kill-session -t "$t" 2>/dev/null     # only our own session: never the whole tmux server
  tmux new-session -d -s "$t" -x 110 -y 30 "env XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS=$DBUS_SESSION_BUS_ADDRESS PATH=$PATH hamstack"
  sleep 3
  grep -q "Run the guided setup now" <(cap) && ok "first run offers the guided setup before anything else" || bad "no first-run offer: $(cap | head -8 | tr '\n' '|')"
  snd n; snd Enter; sleep 2
  check "declining is remembered" test -f ~/.config/hamstack/.declined-first-run
  grep -q "Welcome to hamstack" <(cap) && ok "a fresh install is welcomed" || bad "no welcome screen: $(cap | head -5 | tr '\n' '|')"
  grep -q "Enter" <(cap) && ok "and told how to start the guided setup" || bad "welcome does not mention Enter"
  snd 2; snd a; snd 2                                    # Rigs, add, the dummy radio
  grep -q "Name this rig" <(cap) && ok "adding a rig asks for a name" || bad "no name prompt"
  snd Enter; snd 2; sleep 2                              # accept the name, choose mercury
  expect_out "the rig was added from inside the interface" '"id": "dummy"' cat ~/.config/hamstack/config.json
  snd 4; snd a; snd K 0 T E S T; snd Enter; snd Enter; sleep 2
  expect_out "a station was added from inside the interface" "K0TEST" hamstack stations
  snd 1; snd U; sleep 3; snd S; sleep 6
  for s in rigctld@dummy mercury@dummy pat; do check "$s was started from inside the interface" active "$s"; done
  snd 3; snd a; snd h o m e; snd Enter; sleep 2          # typing a word that is also a key name must still be text
  expect_out "a site named like a key (home) was saved" '"id": "home"' cat ~/.config/hamstack/config.json
  snd 6; sleep 5
  grep -qE "up to date|update available" <(cap) && ok "Programs compares with upstream" || bad "Programs shows no versions: $(cap | sed -n 3,8p | tr '\n' '|')"
  snd q; sleep 1
  fails "q quits the interface" tmux has-session -t "$t"
  tmux kill-session -t "$t" 2>/dev/null
}

# nmea SENTENCE: wraps a sentence body in $...*CS with its checksum.
nmea() { local b="$1" c=0 i; for ((i = 0; i < ${#b}; i++)); do c=$((c ^ $(printf '%d' "'${b:i:1}"))); done; printf '$%s*%02X\r\n' "$b" "$c"; }

# A fake GPS receiver: a pty pair that a background loop writes a fixed position into (the ARRL's
# Newington, Connecticut: FN31pr). gpsd opens the other end like a real receiver's serial port.
gps_fake_start() {
  command -v socat >/dev/null || {
    if command -v apt-get >/dev/null; then sudo apt-get install -y socat >/dev/null 2>&1
    elif command -v dnf >/dev/null; then sudo dnf install -y socat >/dev/null 2>&1
    elif command -v pacman >/dev/null; then sudo pacman -S --noconfirm socat >/dev/null 2>&1; fi; }
  command -v socat >/dev/null || return 1
  rm -f "$HOME/gps-in" "$HOME/gps-dev"
  setsid socat pty,raw,echo=0,link="$HOME/gps-in" pty,raw,echo=0,link="$HOME/gps-dev" >/dev/null 2>&1 &
  echo $! >"$HOME/gps-socat.pid"
  local i; for i in $(seq 1 20); do [ -e "$HOME/gps-dev" ] && [ -e "$HOME/gps-in" ] && break; sleep 0.25; done
  [ -e "$HOME/gps-dev" ] || return 1
  (
    while sleep 0.5; do
      nmea "GPGGA,123519,4142.882,N,07243.632,W,1,08,0.9,545.4,M,46.9,M,,"
      nmea "GPRMC,123519,A,4142.882,N,07243.632,W,022.4,084.4,230394,003.1,W"
      nmea "GPGSA,A,3,04,05,09,12,24,,,,,,,,2.5,1.3,2.1"
    done >"$HOME/gps-in"
  ) >/dev/null 2>&1 &
  echo $! >"$HOME/gps-writer.pid"
}
gps_fake_stop() {
  local f; for f in gps-writer gps-socat; do
    [ -f "$HOME/$f.pid" ] && kill "$(cat "$HOME/$f.pid")" 2>/dev/null; rm -f "$HOME/$f.pid"
  done
  rm -f "$HOME/gps-in" "$HOME/gps-dev"
  pkill -u "$(id -u)" -x socat 2>/dev/null
  return 0
}

sc_gps() {
  need_programs; reset; gps_fake_stop
  fails "no GPS is set up yet" hamstack gps grid --timeout 2s
  expect_out "gps with nothing set up says so" "No GPS set up" hamstack gps
  hamstack install gpsd --yes >/tmp/hamstack-gpsd-install.log 2>&1
  eq "hamstack install gpsd exits 0" "$?" 0
  local g=""; for g in "$(command -v gpsd)" /usr/sbin/gpsd /usr/bin/gpsd; do [ -x "$g" ] && break; done
  check "gpsd is installed" test -x "$g"
  expect_out "installing it again is skipped" "already installed" hamstack install gpsd --yes
  # Debian starts a system-wide gpsd; stop it so it does not hold the fake receiver (doctor reports it below).
  local sys_active=""; sys_active=$(systemctl is-active gpsd.socket 2>/dev/null)
  gps_fake_start || { bad "could not create a fake GPS receiver (socat)"; return; }
  expect_out "gps use rejects a path that is not there" "does not exist" hamstack gps use /nonexistent/gps
  expect_out "gps use takes the fake receiver" "gpsd on port 2948" hamstack gps use "$HOME/gps-dev"
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack rig add dummy --profile dummy >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1
  check "the gpsd unit file was written" test -f "$HOME/.config/systemd/user/hamstack-gpsd.service"
  expect_no "the unit holds no device or port" "ttyACM|gps-dev|2948" cat "$HOME/.config/systemd/user/hamstack-gpsd.service"
  hamstack start gpsd >/dev/null 2>&1
  wait_active gpsd 15; check "gpsd runs as a hamstack user service" active gpsd
  check "on its own port, not gpsd's standard 2947" port_open 2948
  expect_out "gps status shows a 3D fix" "3D fix" bash -c 'for i in $(seq 1 30); do o=$(hamstack gps 2>&1); grep -q "3D fix" <<<"$o" && break; sleep 1; done; echo "$o"'
  expect_out "gps grid reads the position" "FN31pr" hamstack gps grid --timeout 30s
  expect_out "8-character grid" "FN31pr21" hamstack gps grid --timeout 30s --chars 8
  hamstack config set grid EM10 >/dev/null 2>&1
  expect_out "gps grid offers to save when it differs" "Save it with" hamstack gps grid --timeout 30s
  eq "nothing was saved without --save" "$(hamstack config show 2>&1 | grep -c FN31pr)" 0
  expect_out "gps grid --save saves it" "saved as the grid for default" hamstack gps grid --timeout 30s --save
  expect_out "the saved grid is in the config" "FN31pr" hamstack config show
  hamstack pat sync >/dev/null 2>&1
  expect_out "pat sync points Pat's gpsd at hamstack's" '"addr": "localhost:2948"' cat "$HOME/.config/hamstack/pat/me.json"
  expect_out "doctor sees the receiver" "GPS receiver +OK|GPS receiver .*gps-dev" hamstack doctor
  expect_out "doctor sees a fix" "GPS fix +3D fix, grid FN31pr" hamstack doctor
  if [ "$sys_active" = active ]; then
    expect_out "doctor warns about the system-wide gpsd" "system gpsd" hamstack doctor
  else
    expect_no "doctor does not warn when no system gpsd runs" "system gpsd" hamstack doctor
  fi
  # The receiver goes away: the service must stop quietly, not restart in a loop.
  gps_fake_stop; hamstack restart gpsd >/dev/null 2>&1; sleep 9
  eq "an unplugged receiver leaves gpsd stopped" "$(systemctl --user is-active hamstack-gpsd.service)" failed
  eq "and it did not loop" "$(systemctl --user show -p NRestarts --value hamstack-gpsd.service)" 0
  expect_out "doctor says the receiver is missing" "GPS receiver +GPS receiver not found" hamstack doctor
  # An existing gpsd elsewhere: nothing of ours runs.
  hamstack gps use --addr "localhost:2948" >/dev/null 2>&1
  expect_out "an external gpsd is reported as such" "runs elsewhere" hamstack gps
  eq "no gpsd unit is kept for it" "$(ls "$HOME/.config/systemd/user/" | grep -c gpsd)" 0
  hamstack gps remove >/dev/null 2>&1
  expect_out "removing it" "No GPS set up" hamstack gps
}

sc_ax25() {
  reset
  hamstack install direwolf hamlib --yes >/tmp/hamstack-dw.log 2>&1
  hamstack pat off >/dev/null 2>&1
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack rig add pkt --profile dummy --modem direwolf >/dev/null 2>&1
  hamstack units install >/dev/null 2>&1
  hamstack start pkt >/dev/null 2>&1                      # the rig's rigctld and its Direwolf
  wait_active direwolf@pkt 20; check "Direwolf runs" active direwolf@pkt
  check "and offers its AGW port" port_open 8000
  # hamstack's own packet tools: Direwolf's AGW port, no kernel, no root.
  expect_out "packet listen connects to Direwolf's AGW port" "Listening to localhost:8000" timeout 4 hamstack packet listen
  expect_out "packet call dials as the station" "Calling K9TEST as K0TEST" timeout 6 hamstack packet call K9TEST </dev/null
  expect_out "packet call takes digipeaters" "Calling K9TEST via WIDE1-1,WIDE2-1 as K0TEST" timeout 6 hamstack packet call K9TEST WIDE1-1,WIDE2-1 </dev/null
  fails "packet call refuses a bad callsign" hamstack packet call "not a call"
  fails "packet call refuses a bad digipeater" hamstack packet call K9TEST "x;y"
  expect_out "packet listen names the rig that has no Direwolf" "has no Direwolf modem" hamstack packet listen --rig nosuch

  if ! ls /lib/modules/"$(uname -r)"/kernel/net/ax25/ax25.ko* >/dev/null 2>&1 && [ ! -d /sys/module/ax25 ]; then
    expect_out "no kernel AX.25: status says so plainly" "no AX.25 support" hamstack ax25
    fails "no kernel AX.25: setup refuses rather than half-installing" hamstack ax25 setup --yes
    expect_out "and points at the packet tools" "hamstack packet" hamstack ax25 setup --yes
    echo "NOTE: this kernel has no AX.25 module (containers share the host's kernel): kernel steps are skipped here; run this scenario in a VM"
    return
  fi
  expect_out "ax25 status before setup" "not set up" hamstack ax25
  expect_out "setup dry run shows the plan" "axports" hamstack ax25 setup --dry-run
  eq "a dry run changed nothing" "$(grep -c hamstack /etc/ax25/axports 2>/dev/null || echo 0)" 0
  hamstack ax25 setup --yes >/tmp/hamstack-ax25-setup.log 2>&1
  eq "ax25 setup exits 0" "$?" 0
  check "the AX.25 tools are installed" test -x /usr/sbin/kissattach -o -x /usr/bin/kissattach
  expect_out "axports has the port with the station's callsign" "hamstack[[:space:]]+K0TEST[[:space:]]+1200" cat /etc/ax25/axports
  check "the kernel modules are loaded" test -d /sys/module/ax25
  check "and again at boot" test -f /etc/modules-load.d/hamstack-ax25.conf
  for i in $(seq 1 15); do [ -e /tmp/kisstnc ] && break; sleep 1; done
  wait_active direwolf@pkt 20; port_open 8000
  check "Direwolf offers its KISS pseudo-terminal after setup" test -e /tmp/kisstnc
  hamstack ax25 setup --yes >/dev/null 2>&1
  eq "setup is repeatable" "$?" 0
  eq "and does not add the port twice" "$(grep -c '^hamstack' /etc/ax25/axports)" 1
  check "and did not restart Direwolf for nothing" test -e /tmp/kisstnc
  hamstack ax25 attach >/tmp/hamstack-attach.log 2>&1
  eq "ax25 attach exits 0" "$?" 0
  check "kissattach created an AX.25 interface" test -d /sys/class/net/ax0
  expect_out "status shows it attached" "^ +attached" hamstack ax25
  expect_no "ax25 call gets as far as axcall" "not set up|not installed|no AX.25 interface" timeout 5 hamstack ax25 call K9TEST </dev/null
  expect_no "ax25 listen starts axlisten" "no AX.25 port data|not installed" timeout 3 hamstack ax25 listen
  hamstack ax25 detach >/dev/null 2>&1
  sleep 1
  fails "detach removes the interface" test -d /sys/class/net/ax0
}

sc_network() {
  need_programs; reset
  expect_out "network with nothing set up says so" "Nothing is set up yet" hamstack network
  hamstack setup winlink --components rigctld,mercury,pat --radio dummy --callsign K0TEST --ui on --yes --skip-groups --skip-install </dev/null >/dev/null 2>&1
  hamstack stop >/dev/null 2>&1                     # setup --yes starts the services itself: stop them for this check
  for i in $(seq 1 20); do port_closed 8300 && break; sleep 1; done
  expect_out "not running yet is shown as such" "mercury@dummy +HF modem: ARQ control.* +8300 +not running" hamstack network
  hamstack start >/dev/null 2>&1
  wait_active pat 20; port_open 8300
  expect_out "rigctld is local only" "rigctld@dummy +rig control \(hamlib\) +4532 +this computer only" hamstack network
  expect_out "Mercury's ARQ port is live" "8300 +(this computer only|ANY computer on your network)" hamstack network
  expect_out "Mercury's web UI is flagged as open to the network without a password" "web UI +10000 +ANY computer on your network +no password" hamstack network
  expect_out "and the warning says what to do" "hamstack config set ui off" hamstack network
  expect_out "Pat's web UI is listed" "pat +Winlink web UI \(K0TEST\) +8080 +this computer only" hamstack network
  expect_out "this computer's address is shown" "This computer: [0-9]+\." hamstack network
  expect_out "the AX.25 state is shown" "AX.25 \(the Linux kernel's packet stack\)" hamstack network
}

sc_apps() {
  reset
  expect_out "install --list shows every program" "fldigi .*wsjtx-improved .*js8call .*xastir" bash -c 'hamstack install --list | tr "\n" " "'
  expect_out "and how each would be installed here" "(distribution package|built from source|AUR only)" hamstack install --list
  expect_out "versions lists the new programs too" "flrig" hamstack versions --offline
  expect_out "a dependency is pulled in, with the reason" "also installing hamlib \(needed by mercury\)" hamstack install mercury --dry-run
  expect_out "suggestions are listed, never added silently" "Suggested with these" hamstack install fldigi --dry-run
  expect_no "and not installed unasked" "Install flrig|flrig" bash -c 'hamstack install fldigi --dry-run 2>&1 | sed -n "/Plan:/,\$p"'
  fails "conflicting programs refuse to install together" hamstack install wsjtx wsjtx-improved --dry-run
  expect_out "and say which" "wsjtx.*wsjtx-improved|cannot both be installed" hamstack install wsjtx wsjtx-improved --dry-run
  fails "an unknown program is refused" hamstack install nosuchprogram --dry-run
  if command -v pacman >/dev/null; then
    expect_out "Arch: a program only the AUR has says so, and does not build AUR packages" "only in the AUR" hamstack install ax25 --yes
    expect_out "Arch: but it can be built from source" "Building xastir .* from source" hamstack install xastir --from-source --dry-run
  else
    hamstack install flamp --yes >/tmp/hamstack-flamp.log 2>&1
    eq "a distribution package installs" "$?" 0
    expect_out "and is then known with its version" "flamp +[0-9]+\.[0-9]+" hamstack versions --offline
    expect_out "a source build is planned for the newest release" "Download flamp .* source" hamstack install flamp --from-source --dry-run
    expect_out "with the disclaimer" "installs compilers and development libraries" hamstack install flamp --from-source --dry-run
  fi
  # The guided setup's last choice installs programs and sets nothing up.
  if ! command -v pacman >/dev/null; then
    printf '8\nflmsg\n\ny\n' | hamstack setup winlink >/tmp/hamstack-pick.log 2>&1
    expect_out "setup can just install chosen programs" "(already installed|Install flmsg)" cat /tmp/hamstack-pick.log
    check "and sets nothing up" test ! -e "$HOME/.config/hamstack/config.json"
  fi
  # A rig's Direwolf can be configured.
  hamstack pat off >/dev/null 2>&1
  hamstack station add me --callsign K0TEST >/dev/null 2>&1
  hamstack rig add pkt --profile dummy --modem direwolf >/dev/null 2>&1
  expect_out "direwolf show gives the defaults" "modem speed:  1200 baud \(the default\)" hamstack direwolf show
  hamstack direwolf set pkt baud 9600 >/dev/null 2>&1; hamstack direwolf set pkt call K0TEST-10 >/dev/null 2>&1; hamstack direwolf set pkt txdelay 40 >/dev/null 2>&1
  expect_out "settings reach the generated configuration" "MODEM 9600" hamstack direwolf show
  expect_out "with the callsign and SSID" "MYCALL K0TEST-10" hamstack direwolf show
  expect_out "and the timing" "TXDELAY 40" hamstack direwolf show
  fails "a bad setting is refused" hamstack direwolf set pkt baud 1234
  expect_out "digipeating comes with a warning" "harms a whole area" hamstack direwolf set pkt digipeat on
  expect_out "and writes the digipeater rule" "DIGIPEAT 0 0" hamstack direwolf show
  hamstack direwolf set pkt digipeat off >/dev/null 2>&1
  mkdir -p "$HOME/.config/hamstack/rigs/pkt"; echo "PBEACON delay=1 every=30" > "$HOME/.config/hamstack/rigs/pkt/direwolf.extra"
  expect_out "the user's own lines are kept, last" "your own lines: 1" hamstack direwolf show
}

# ---------------------------------------------------------------------------------------------------------

ALL="install setup multirig sites migrate direwolf doctor groups linger patsync gps ax25 network apps tui"

run_scenario() {
  local name="$1" fn="sc_${1//-/_}" p0=$PASS f0=$FAILS t0=$SECONDS
  if ! declare -F "$fn" >/dev/null; then echo "FAIL: no scenario named $name"; FAILS=$((FAILS + 1)); return; fi
  echo "=== $name"
  "$fn"
  echo "SCENARIO $name pass=$((PASS - p0)) fail=$((FAILS - f0)) seconds=$((SECONDS - t0))"
}

main() {
  local list=("$@")
  [ ${#list[@]} -eq 0 ] && read -ra list <<<"$ALL"
  for s in "${list[@]}"; do run_scenario "$s"; done
  echo "TOTAL pass=$PASS fail=$FAILS"
  [ "$FAILS" -eq 0 ]
}
main "$@"
