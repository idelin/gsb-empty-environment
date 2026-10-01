"""Fault-tolerant replica placement with verifiable certificates.

Given servers (each belonging to a rack and an availability zone, with a
capacity and a per-replica cost), place r replicas of one data shard on
distinct servers such that after the failure of ANY single specified
failure set, at least q replicas remain. Minimise total replica cost.

The solver is an exact exhaustive enumeration over all C(n, r) candidate
placements, so a returned placement is provably cost-optimal and an
"infeasible" verdict is backed by auditable enumeration statistics
instead of an unverifiable claim.

Requires Python 3.11+ (standard library only).

CLI:
    python3.11 replica_placement.py solve  INPUT.json [-o CERT.json]
    python3.11 replica_placement.py verify INPUT.json CERT.json
"""

from __future__ import annotations

import argparse
import itertools
import json
import sys
from math import comb

SCHEMA_VERSION = 1


# ---------------------------------------------------------------------------
# Input validation
# ---------------------------------------------------------------------------

class SpecError(ValueError):
    """Raised when the placement specification is invalid."""


def _require(cond: bool, msg: str) -> None:
    if not cond:
        raise SpecError(msg)


def validate_spec(spec: dict) -> None:
    _require(isinstance(spec, dict), "spec must be a JSON object")
    r = spec.get("r")
    q = spec.get("q")
    _require(isinstance(r, int) and not isinstance(r, bool) and r >= 1,
             "r must be an integer >= 1")
    _require(isinstance(q, int) and not isinstance(q, bool) and q >= 1,
             "q must be an integer >= 1")
    _require(q <= r, f"q ({q}) must be <= r ({r}): cannot keep q replicas "
                     "after a failure if fewer than q are placed")

    servers = spec.get("servers")
    _require(isinstance(servers, list) and len(servers) >= 1,
             "servers must be a non-empty list")
    seen = set()
    for i, s in enumerate(servers):
        _require(isinstance(s, dict), f"servers[{i}] must be an object")
        sid = s.get("id")
        _require(isinstance(sid, str) and sid, f"servers[{i}].id must be a non-empty string")
        _require(sid not in seen, f"duplicate server id: {sid!r}")
        seen.add(sid)
        for field in ("rack", "zone"):
            _require(isinstance(s.get(field), str) and s[field],
                     f"servers[{i}].{field} must be a non-empty string")
        cap = s.get("capacity")
        _require(isinstance(cap, int) and not isinstance(cap, bool) and cap >= 0,
                 f"servers[{i}].capacity must be an integer >= 0")
        cost = s.get("cost")
        _require(isinstance(cost, (int, float)) and not isinstance(cost, bool)
                 and cost >= 0,
                 f"servers[{i}].cost must be a number >= 0")

    failure_sets = spec.get("failure_sets", [])
    _require(isinstance(failure_sets, list), "failure_sets must be a list")
    fs_names = set()
    for i, fs in enumerate(failure_sets):
        _require(isinstance(fs, dict), f"failure_sets[{i}] must be an object")
        name = fs.get("name")
        _require(isinstance(name, str) and name,
                 f"failure_sets[{i}].name must be a non-empty string")
        _require(name not in fs_names, f"duplicate failure set name: {name!r}")
        fs_names.add(name)
        members = fs.get("servers")
        _require(isinstance(members, list) and len(members) >= 1,
                 f"failure_sets[{i}].servers must be a non-empty list")
        _require(len(set(members)) == len(members),
                 f"failure_sets[{i}].servers contains duplicates")
        for m in members:
            _require(m in seen,
                     f"failure_sets[{i}] references unknown server {m!r}")


# ---------------------------------------------------------------------------
# Exact solver
# ---------------------------------------------------------------------------

def enumerate_placements(spec: dict):
    """Evaluate every C(n, r) candidate placement exactly once.

    Returns (best, stats) where best is (total_cost, sorted_id_tuple) or
    None when no feasible placement exists, and stats is a dict of
    exhaustive enumeration counters such that:

        combinations_evaluated
            == rejected_capacity + rejected_fault + feasible
            == C(len(servers), r)
    """
    servers = spec["servers"]
    r = spec["r"]
    q = spec["q"]
    fs_members = [(fs["name"], frozenset(fs["servers"]))
                  for fs in spec.get("failure_sets", [])]

    stats = {
        "servers_total": len(servers),
        "combinations_total": comb(len(servers), r) if len(servers) >= r else 0,
        "combinations_evaluated": 0,
        "rejected_capacity": 0,
        "rejected_fault": 0,
        "rejected_fault_by_set": {name: 0 for name, _ in fs_members},
        "feasible": 0,
    }

    best = None  # (cost, ids) with deterministic tie-break on ids
    for combo in itertools.combinations(servers, r):
        stats["combinations_evaluated"] += 1

        # Capacity: a server with capacity < 1 cannot host a replica.
        if any(s["capacity"] < 1 for s in combo):
            stats["rejected_capacity"] += 1
            continue

        # Fault tolerance: after any specified failure set dies, the
        # replicas hosted on its members are lost; at least q must remain.
        blocking = None
        for name, members in fs_members:
            lost = sum(1 for s in combo if s["id"] in members)
            if r - lost < q:
                blocking = name
                break
        if blocking is not None:
            stats["rejected_fault"] += 1
            stats["rejected_fault_by_set"][blocking] += 1
            continue

        stats["feasible"] += 1
        cost = sum(s["cost"] for s in combo)
        ids = tuple(sorted(s["id"] for s in combo))
        if best is None or (cost, ids) < best:
            best = (cost, ids)

    return best, stats


def failure_witnesses(spec: dict, placement: list[str]) -> list[dict]:
    """Per-failure-set witness: how many replicas survive that failure."""
    r = spec["r"]
    q = spec["q"]
    placed = set(placement)
    witnesses = []
    for fs in spec.get("failure_sets", []):
        members = set(fs["servers"])
        lost = len(placed & members)
        remaining = r - lost
        witnesses.append({
            "failure_set": fs["name"],
            "replicas_lost": lost,
            "replicas_remaining": remaining,
            "required_remaining": q,
            "holds": remaining >= q,
        })
    return witnesses


def solve(spec: dict) -> dict:
    """Solve and return a certificate (or auditable infeasibility stats)."""
    validate_spec(spec)
    r, q = spec["r"], spec["q"]
    best, stats = enumerate_placements(spec)

    base = {
        "schema_version": SCHEMA_VERSION,
        "method": "exhaustive-enumeration",
        "r": r,
        "q": q,
        "statistics": stats,
    }

    if best is None:
        # No fake certificate: only reproducible enumeration statistics.
        base["status"] = "infeasible"
        base["placement"] = None
        base["total_cost"] = None
        base["note"] = (
            "No feasible placement exists. The statistics above are a "
            "complete, re-runnable account of the exhaustive search: every "
            "C(n, r) candidate was evaluated and rejected for the recorded "
            "reason. Re-run `verify` to independently confirm."
        )
        return base

    cost, ids = best
    placement = list(ids)
    base.update({
        "status": "optimal",
        "placement": placement,
        "total_cost": cost,
        "optimality": "proven-by-exhaustive-enumeration",
        "failure_witnesses": failure_witnesses(spec, placement),
    })
    return base


# ---------------------------------------------------------------------------
# Independent verification
# ---------------------------------------------------------------------------

def verify(spec: dict, cert: dict) -> list[str]:
    """Re-check a certificate from scratch. Returns a list of problems
    (empty list means the certificate is fully verified)."""
    errors = []

    def err(msg: str) -> None:
        errors.append(msg)

    try:
        validate_spec(spec)
    except SpecError as exc:
        return [f"invalid spec: {exc}"]

    if cert.get("schema_version") != SCHEMA_VERSION:
        err(f"unsupported schema_version: {cert.get('schema_version')!r}")
    if cert.get("method") != "exhaustive-enumeration":
        err(f"unknown method: {cert.get('method')!r}")
    if cert.get("r") != spec["r"] or cert.get("q") != spec["q"]:
        err("certificate r/q do not match the spec")

    # Re-run the exhaustive search independently.
    best, stats = enumerate_placements(spec)
    if cert.get("statistics") != stats:
        err("enumeration statistics do not match an independent re-run "
            f"(cert={cert.get('statistics')!r} recomputed={stats!r})")

    status = cert.get("status")
    if status == "infeasible":
        if best is not None:
            err(f"certificate claims infeasible but a feasible placement "
                f"exists: {list(best[1])} with cost {best[0]}")
        if cert.get("placement") is not None:
            err("infeasible certificate must not contain a placement")
    elif status == "optimal":
        placement = cert.get("placement")
        if not isinstance(placement, list):
            err("optimal certificate lacks a placement list")
        else:
            by_id = {s["id"]: s for s in spec["servers"]}
            if len(placement) != spec["r"]:
                err(f"placement has {len(placement)} servers, r={spec['r']} required")
            if len(set(placement)) != len(placement):
                err("placement contains duplicate servers")
            for sid in placement:
                if sid not in by_id:
                    err(f"placement uses unknown server {sid!r}")
                elif by_id[sid]["capacity"] < 1:
                    err(f"server {sid!r} has capacity < 1")
            known = [sid for sid in placement if sid in by_id]
            recomputed_cost = sum(by_id[sid]["cost"] for sid in known)
            if cert.get("total_cost") != recomputed_cost:
                err(f"total_cost mismatch: cert={cert.get('total_cost')!r} "
                    f"recomputed={recomputed_cost!r}")
            witnesses = failure_witnesses(spec, placement)
            for w in witnesses:
                if not w["holds"]:
                    err(f"failure set {w['failure_set']!r} leaves "
                        f"{w['replicas_remaining']} replicas < q={w['required_remaining']}")
            if cert.get("failure_witnesses") != witnesses:
                err("failure_witnesses do not match recomputation")
            if best is None:
                err("no feasible placement exists, yet certificate claims optimal")
            elif cert.get("total_cost") != best[0]:
                err(f"claimed cost {cert.get('total_cost')!r} is not optimal; "
                    f"exhaustive search found {best[0]!r} via {list(best[1])}")
    else:
        err(f"unknown status: {status!r}")

    return errors


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _load_json(path: str):
    try:
        if path == "-":
            return json.load(sys.stdin)
        with open(path, "r", encoding="utf-8") as fh:
            return json.load(fh)
    except OSError as exc:
        raise SystemExit(f"error: cannot read {path}: {exc}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"error: invalid JSON in {path}: {exc}")


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        prog="replica_placement",
        description="Fault-tolerant replica placement with verifiable "
                    "certificates (exact exhaustive solver).")
    sub = parser.add_subparsers(dest="command", required=True)

    p_solve = sub.add_parser("solve", help="solve and emit a certificate JSON")
    p_solve.add_argument("input", help="spec JSON file ('-' for stdin)")
    p_solve.add_argument("-o", "--output", help="write certificate here "
                                                "(default: stdout)")

    p_verify = sub.add_parser("verify", help="independently verify a certificate")
    p_verify.add_argument("input", help="spec JSON file")
    p_verify.add_argument("certificate", help="certificate JSON file ('-' for stdin)")

    args = parser.parse_args(argv)

    if args.command == "solve":
        spec = _load_json(args.input)
        try:
            cert = solve(spec)
        except SpecError as exc:
            print(f"error: invalid spec: {exc}", file=sys.stderr)
            return 2
        text = json.dumps(cert, indent=2, sort_keys=True) + "\n"
        if args.output:
            with open(args.output, "w", encoding="utf-8") as fh:
                fh.write(text)
        else:
            sys.stdout.write(text)
        return 0 if cert["status"] == "optimal" else 1

    # verify
    spec = _load_json(args.input)
    cert = _load_json(args.certificate)
    errors = verify(spec, cert)
    if errors:
        print("CERTIFICATE INVALID:", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        return 1
    print(f"certificate verified: status={cert['status']} "
          f"(exhaustive re-enumeration matched)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
