"""Independent validator for merger state directories.

Replays the event log from scratch (sharing only the log *format* with
the merger, none of its state machinery) and audits:

1. log integrity      - hash chain, checksums, torn/partial final writes
2. no double counting - publish vectors are lineage-disjoint and cover
                        exactly the live shard set at that point in time
3. faithful values    - published values re-sum from the referenced
                        slices; revoked slices never contribute
4. compensation rules - corrections appear only as chained compensation
                        events; exactly one ``result`` publish per window
5. ack discipline     - every ack matches a prior publish, no double ack
6. deterministic ids  - result ids recompute from their bound content

CLI::

    python -m winmerge.validator check     <state_dir>
    python -m winmerge.validator snapshot  <state_dir> --out snap.json
    python -m winmerge.validator verify    <state_dir> --snapshot snap.json
"""
from __future__ import annotations

import argparse
import hashlib
import json
import sys

from .store import GENESIS, canonical, read_log


class Finding:
    def __init__(self, ok: bool, check: str, detail: str):
        self.ok = ok
        self.check = check
        self.detail = detail

    def __str__(self):
        mark = "PASS" if self.ok else "FAIL"
        return f"[{mark}] {self.check}: {self.detail}"


def _result_id(core: dict) -> str:
    return "res-" + hashlib.sha256(canonical(core).encode()).hexdigest()[:16]


def _lineage_maps(handoffs):
    parent_of = {}
    for h in handoffs:
        for parent, children in h["splits"].items():
            for child in children:
                parent_of[child] = parent
    return parent_of


def _ancestors(parent_of, shard):
    out = set()
    while shard in parent_of:
        shard = parent_of[shard]
        out.add(shard)
    return out


def _leaves(initial_shards, handoffs):
    current = set(initial_shards)
    for h in sorted(handoffs, key=lambda x: x["epoch"]):
        for parent, children in h["splits"].items():
            current.discard(parent)
            current.update(children)
    return frozenset(current)


def audit(state_dir: str, initial_shards: list[str]) -> list[Finding]:
    findings: list[Finding] = []
    result = read_log(f"{state_dir}/events.jsonl")

    # 1. log integrity -------------------------------------------------
    findings.append(Finding(
        result.corruption is None,
        "log-integrity",
        result.corruption or f"hash chain intact over {len(result.records)} records",
    ))
    findings.append(Finding(
        result.torn_tail is None,
        "torn-tail",
        "no partial write at tail"
        if result.torn_tail is None
        else f"partial final write detected ({len(result.torn_tail)} bytes): "
             "recovery must ignore it",
    ))
    if result.corruption:
        return findings

    # sequential replay with independent state -------------------------
    handoffs: list[dict] = []
    slices: dict[str, dict[str, dict[int, dict]]] = {}
    revoked: dict[str, set[tuple[str, int]]] = {}
    publishes: dict[str, list[dict]] = {}
    acked: set[str] = set()
    known_ids: set[str] = set()
    double_count_bad = []
    value_bad = []
    comp_bad = []
    ack_bad = []
    id_bad = []

    for rec in result.records:
        p = rec["payload"]
        rec_type = p.get("rec")
        if rec_type == "slice_accepted":
            slices.setdefault(p["window"], {}).setdefault(p["shard"], {})[
                p["local_version"]
            ] = p
        elif rec_type == "slice_revoked":
            revoked.setdefault(p["window"], set()).add(
                (p["shard"], p["local_version"])
            )
        elif rec_type == "handoff_registered":
            handoffs.append(p)
        elif rec_type == "publish":
            window = p["window"]
            prior = publishes.setdefault(window, [])
            parent_of = _lineage_maps(handoffs)
            vector = p["version_vector"]
            # 2. coverage + lineage disjointness
            live = _leaves(initial_shards, handoffs)
            if not p["retracted"]:
                if set(vector) != set(live):
                    double_count_bad.append(
                        f"{p['result_id']}: vector {sorted(vector)} != live "
                        f"shards {sorted(live)}"
                    )
                seen: list[str] = []
                for shard in vector:
                    for other in seen:
                        if (
                            shard in _ancestors(parent_of, other)
                            or other in _ancestors(parent_of, shard)
                        ):
                            double_count_bad.append(
                                f"{p['result_id']}: shards {other} and {shard} "
                                "overlap in lineage (ancestor/descendant) in "
                                "the same window"
                            )
                    seen.append(shard)
            # 3. recompute value from referenced slices
            if not p["retracted"]:
                total = 0
                ok = True
                win_revoked = revoked.get(window, set())
                for shard, lv in vector.items():
                    sl = slices.get(window, {}).get(shard, {}).get(lv)
                    if sl is None:
                        value_bad.append(
                            f"{p['result_id']}: references unseen slice "
                            f"{shard} v{lv}"
                        )
                        ok = False
                        break
                    if (shard, lv) in win_revoked:
                        value_bad.append(
                            f"{p['result_id']}: references revoked slice "
                            f"{shard} v{lv}"
                        )
                        ok = False
                        break
                    total += sl["value"]
                if ok and total != p["value"]:
                    value_bad.append(
                        f"{p['result_id']}: value {p['value']} != "
                        f"recomputed {total}"
                    )
            # 4. compensation discipline
            if not prior:
                if p["kind"] != "result":
                    comp_bad.append(f"{p['result_id']}: first publish not 'result'")
            else:
                prev = prior[-1]
                prev_value = 0 if prev["retracted"] else prev["value"]
                if p["kind"] != "compensation":
                    comp_bad.append(
                        f"{p['result_id']}: second full publish for {window} "
                        "would rewrite history"
                    )
                elif p["base_result_id"] != prev["result_id"]:
                    comp_bad.append(
                        f"{p['result_id']}: base {p['base_result_id']} does not "
                        f"chain to {prev['result_id']}"
                    )
                expect_delta = (
                    -prev_value if p["retracted"] else p["value"] - prev_value
                )
                if p["delta"] != expect_delta:
                    comp_bad.append(
                        f"{p['result_id']}: delta {p['delta']} != {expect_delta}"
                    )
            # 6. deterministic id
            core = {
                "kind": p["kind"],
                "window": p["window"],
                "rule_version": p["rule_version"],
                "epoch": p["epoch"],
                "version_vector": p["version_vector"],
                "value": p["value"],
                "comp_index": p["comp_index"],
            }
            if _result_id(core) != p["result_id"]:
                id_bad.append(f"{p['result_id']}: id does not match content")
            prior.append(p)
            known_ids.add(p["result_id"])
        elif rec_type == "publish_acked":
            rid = p["result_id"]
            if rid not in known_ids:
                ack_bad.append(f"ack for unknown result {rid}")
            elif rid in acked:
                ack_bad.append(f"result {rid} acked twice")
            acked.add(rid)

    findings.append(Finding(
        not double_count_bad, "no-double-count",
        "every publish covers the live shard set exactly once"
        if not double_count_bad else "; ".join(double_count_bad),
    ))
    findings.append(Finding(
        not value_bad, "faithful-values",
        "all published values re-sum from compatible slices"
        if not value_bad else "; ".join(value_bad),
    ))
    findings.append(Finding(
        not comp_bad, "compensation-discipline",
        "corrections are chained compensations, history untouched"
        if not comp_bad else "; ".join(comp_bad),
    ))
    findings.append(Finding(
        not ack_bad, "ack-discipline",
        "every ack matches exactly one publish"
        if not ack_bad else "; ".join(ack_bad),
    ))
    findings.append(Finding(
        not id_bad, "deterministic-ids",
        "all result ids recompute from bound content"
        if not id_bad else "; ".join(id_bad),
    ))
    return findings


def snapshot(state_dir: str, out_path: str) -> dict:
    result = read_log(f"{state_dir}/events.jsonl")
    if result.corruption:
        raise SystemExit(f"cannot snapshot corrupt log: {result.corruption}")
    snap = {
        "seq": result.records[-1]["seq"] if result.records else 0,
        "head": result.records[-1]["checksum"] if result.records else GENESIS,
    }
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(snap, fh, indent=2)
    return snap


def verify_snapshot(state_dir: str, snap_path: str) -> bool:
    with open(snap_path, "r", encoding="utf-8") as fh:
        snap = json.load(fh)
    result = read_log(f"{state_dir}/events.jsonl")
    if result.corruption or result.torn_tail:
        return False
    if len(result.records) < snap["seq"]:
        return False
    if snap["seq"] == 0:
        return snap["head"] == GENESIS
    return result.records[snap["seq"] - 1]["checksum"] == snap["head"]


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(prog="winmerge.validator")
    sub = ap.add_subparsers(dest="cmd", required=True)
    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("state_dir")
    common.add_argument(
        "--initial-shards", default="A,B",
        help="comma-separated epoch-1 shard set (default: A,B)",
    )
    sub.add_parser("check", parents=[common])
    sp = sub.add_parser("snapshot", parents=[common])
    sp.add_argument("--out", required=True)
    vp = sub.add_parser("verify", parents=[common])
    vp.add_argument("--snapshot", required=True)
    args = ap.parse_args(argv)

    if args.cmd == "check":
        findings = audit(args.state_dir, args.initial_shards.split(","))
        for f in findings:
            print(f)
        ok = all(f.ok for f in findings)
        print("AUDIT", "OK" if ok else "FAILED")
        return 0 if ok else 1
    if args.cmd == "snapshot":
        snap = snapshot(args.state_dir, args.out)
        print(f"snapshot at seq {snap['seq']}: {snap['head'][:16]}... -> {args.out}")
        return 0
    if args.cmd == "verify":
        ok = verify_snapshot(args.state_dir, args.snapshot)
        print("snapshot verify:", "OK (history prefix unchanged)" if ok else "FAILED")
        return 0 if ok else 1
    return 2


if __name__ == "__main__":
    sys.exit(main())
