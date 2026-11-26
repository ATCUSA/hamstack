#!/usr/bin/env python3
"""Bundle openRMS Rigs profiles (CC-BY-SA-4.0) into internal/radiosetup/profiles.json.

usage: scripts/bundle-radio-profiles.py PATH/TO/openRMS-Rigs/profiles [commit]

Keeps what hamstack's radio settings check needs: each step's machine fields, its note, confidence and source ids,
and each source's id, title, URL and date. Leaves out authors, and note sentences that name a person by callsign or
name, because hamstack's repository holds no real person's callsign (AGENTS.md, rule 1). The credit and licence go in
internal/radiosetup/NOTICE.md.
"""
import json, os, re, sys

import yaml  # only this script needs it; hamstack itself reads the JSON

CALL = re.compile(r"\b[A-Z]{1,2}[0-9][A-Z]{1,4}\b")
STEP_KEYS = ["id", "kind", "name", "value", "expect", "display", "unit", "safety", "check_only", "cat", "manual", "note", "confidence", "sources", "passband_hz"]


def clean(text):
    if not text:
        return text
    keep = [s for s in re.split(r"(?<=[.!?])\s+", str(text).strip()) if not CALL.search(s)]
    return " ".join(keep).strip()


CALL_ANYCASE = re.compile(r"(?i)(?<![a-z0-9])[a-z]{1,2}[0-9][a-z]{1,4}(?![a-z0-9])")


def source(s):
    out = {"id": s.get("id", "")}
    if s.get("title"):
        out["title"] = re.sub(r"\s{2,}", " ", CALL.sub("", s["title"])).strip(" :-")
    if s.get("date"):
        out["date"] = str(s["date"])
    url = s.get("url", "")
    if url and not CALL_ANYCASE.search(url.split("://", 1)[-1]):
        out["url"] = url  # a link with a callsign in it (a personal site) is left out
    return out


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    root, commit = sys.argv[1], (sys.argv[2] if len(sys.argv) > 2 else "")
    out = []
    for folder in sorted(os.listdir(root)):
        d = os.path.join(root, folder)
        if not os.path.isdir(d):
            continue
        for f in sorted(os.listdir(d)):
            if not f.endswith(".yaml") or f.startswith("_"):
                continue
            p = yaml.safe_load(open(os.path.join(d, f)))
            steps = []
            for s in p.get("steps", []):
                st = {k: s[k] for k in STEP_KEYS if k in s}
                if "note" in st:
                    st["note"] = clean(st["note"])
                    if not st["note"]:
                        del st["note"]
                steps.append(st)
            out.append({
                "id": folder + "/" + f[:-5],
                "title": p.get("title", ""),
                "operation": p.get("operation", ""),
                "hamlib_model": p.get("rig", {}).get("hamlib_model", 0),
                "rig": p.get("rig", {}).get("name", ""),
                "audio": {k: v for k, v in (p.get("audio") or {}).items() if k == "rx_target_dbfs"},
                "steps": steps,
                "sources": [source(s) for s in p.get("sources", [])],
                "tags": p.get("tags", []),
            })
    here = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "radiosetup")
    json.dump({"commit": commit, "licence": "CC-BY-SA-4.0", "credit": "openRMS Rigs contributors", "profiles": out},
              open(os.path.join(here, "profiles.json"), "w"), indent=1, ensure_ascii=False)
    print(f"{len(out)} profiles")


main()
