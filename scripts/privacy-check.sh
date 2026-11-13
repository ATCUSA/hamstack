#!/usr/bin/env bash
# Fails if a tracked file looks like it holds personal data. hamstack is a public project used by licensed
# operators: examples and tests must use neutral values (callsigns N0CALL, K0TEST, K0EOC, K1ABC; grids FN31pr,
# EM10dg; home directory /home/u), never a real person's.
#
# Two layers:
#   1. generic checks that need no knowledge of anyone: real-looking email addresses, other people's home
#      directories, private keys and tokens;
#   2. a private deny list: a file named .privacy-deny in the repository root (it is gitignored, so it is never
#      published) holding one extended regular expression per line, for YOUR OWN callsign, grid square, name or
#      domain. Add yours once; the check then guards every commit.
#
# Usage: scripts/privacy-check.sh [--staged]     (no argument: every tracked file; --staged: only what is about to be committed)
set -u
cd "$(git rev-parse --show-toplevel)" || exit 2

if [ "${1:-}" = "--staged" ]; then
  mapfile -d '' files < <(git diff --cached --name-only --diff-filter=ACM -z)
else
  mapfile -d '' files < <(git ls-files -z)
fi
[ ${#files[@]} -gt 0 ] || exit 0

fail=0
report() { echo "privacy-check: $1"; fail=1; }

# Never inspect this script itself for its own example patterns.
files=("${files[@]/scripts\/privacy-check.sh/}")

# 1a. email addresses other than the obviously public or placeholder ones
emails=$(grep -nIEoH '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' -- "${files[@]}" 2>/dev/null \
  | grep -viE '@(users\.noreply\.github\.com|noreply\.github\.com|example\.(com|org|net)|anthropic\.com)|noreply@|@v[0-9]|@[A-Za-z0-9_-]*\.(service|socket|timer|target|path|mount)\b' || true)
if [ -n "$emails" ]; then report "email addresses found (use noreply or example.com):"; echo "$emails" | head -10; fi

# 1b. someone's home directory (the tests use /home/u)
homes=$(grep -nIEoH '/home/[a-z][a-z0-9_-]*/' -- "${files[@]}" 2>/dev/null | grep -vE '/home/(u|user|tester|runner)/' || true)
if [ -n "$homes" ]; then report "home directories of real users found (use /home/u):"; echo "$homes" | head -10; fi

# 1c. keys and tokens
secrets=$(grep -nIEH 'BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}' -- "${files[@]}" 2>/dev/null || true)
if [ -n "$secrets" ]; then report "something that looks like a key or token:"; echo "$secrets" | cut -c1-120 | head -5; fi

# 2. the maintainer's private deny list
if [ -f .privacy-deny ]; then
  while IFS= read -r pat; do
    case "$pat" in ''|'#'*) continue ;; esac
    hits=$(grep -nIEiH -e "$pat" -- "${files[@]}" 2>/dev/null || true)
    if [ -n "$hits" ]; then report "matches your private deny list (/$pat/):"; echo "$hits" | cut -c1-140 | head -5; fi
  done < .privacy-deny
fi

if [ $fail -eq 0 ]; then echo "privacy-check: ok (${#files[@]} files)"; fi
exit $fail
