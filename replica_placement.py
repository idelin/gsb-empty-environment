#!/usr/bin/env python3.11
"""Fault-tolerant replica placement with verifiable certificates.

Servers belong to racks and availability zones, have a capacity (max number
of replicas they may host) and a per-replica cost. Each data shard must be
placed on r distinct servers such that, after the failure of any one
specified fault set, at least q replicas survive (i.e. are located on
servers outside that fault set). The objective is to minimize total
placement cost.

The solver uses exhaustive enumeration, so an OPTIMAL certificate is
provably optimal and an INFEASIBLE answer comes with reproducible
exhaustive statistics instead of a fake certificate.

Usage:
    python3.11 replica_placement.py solve  input.json [-o certificate.json]
    python3.11 replica_placement.py verify input.json certificate.json [--check-optimality]

See README.md for the input/output format specification.
"""

from __future__ import annotations

import argparse
import itertools
import json
import sys
from typing import Any

FORMAT_VERSION = 1


# ---------------------------------------------------------------------------
# Input handling
# ---------------------------------------------------------------------------

class InputError(ValueError):
    """Raised when the problem instance is malformed."""


def load_instance(data: dict[str, Any]) -> dict[str, Any]:
    """Validate and normalize a problem instance."""
    if not isinstance(data, dict):
        raise InputError("top-level JSON value must be an object")

    servers_raw = data.get("servers")
    if not isinstance(servers_raw, list) or not servers_raw:
        raise InputError("'servers' must be a non-empty list")

    servers: dict[str, dict[str, Any]] = {}
    for entry in servers_raw:
        if not isinstance(entry, dict):
            raise InputError("each server must be an object")
        sid = entry.get("id")
        if not isinstance(sid, str) or not sid:
            raise InputError("each server needs a non-empty string 'id'")
        if sid in servers:
            raise InputError(f"duplicate server id: {sid!r}")
        capacity = entry.get("capacity")
        cost = entry.get("cost")
        if not isinstance(capacity, int) or isinstance(capacity, bool) or capacity < 0:
            raise InputError(f"server {sid!r}: 'capacity' must be a non-negative integer")
        if not isinstance(cost, (int, float)) or isinstance(cost, bool) or cost < 0:
            raise InputError(f"server {sid!r}: 'cost' must be a non-negative number")
        servers[sid] = {
            "id": sid,
            "rack": entry.get("rack"),
            "zone": entry.get("zone"),
            "capacity": capacity,
            "cost": cost,
        }

    r = data.get("r")
    q = data.get("q")
    if not isinstance(r, int) or isinstance(r, bool) or r < 1:
        raise InputError("'r' must be a positive integer")
    if not isinstance(q, int) or isinstance(q, bool) or q < 0 or q > r:
        raise InputError("'q' must be an integer with 0 <= q <= r")

    fault_sets: dict[str, frozenset[str]] = {}
    for entry in data.get("fault_sets", []):
        if not isinstance(entry, dict):
            raise InputError("each fault set must be an object")
        fid = entry.get("id")
        members = entry.get("servers")
        if not isinstance(fid, str) or not fid:
            raise InputError("each fault set needs a non-empty string 'id'")
        if not isinstance(members, list) or not all(isinstance(m, str) for m in members):
            raise InputError(f"fault set {fid!r}: 'servers' must be a list of server ids")
        unknown = [m for m in members if m not in servers]
        if unknown:
            raise InputError(f"fault set {fid!r}: unknown servers {unknown!r}")
        if fid in fault_sets:
            raise InputError(f"duplicate fault set id: {fid!r}")
        fault_sets[fid] = frozenset(members)

    # Optionally derive one fault set per rack and/or per zone.
    for dim in data.get("derive_fault_sets", []):
        if dim not in ("rack", "zone"):
            raise InputError(f"'derive_fault_sets' entries must be 'rack' or 'zone', got {dim!r}")
        groups: dict[Any, list[str]] = {}
        for s in servers.values():
            key = s[dim]
            if key is not None:
                groups.setdefault(key, []).append(s["id"])
        for key, members in sorted(groups.items(), key=lambda kv: str(kv[0])):
            fid = f"{dim}:{key}"
            if fid in fault_sets:
                raise InputError(f"derived fault set id collides with explicit one: {fid!r}")
            fault_sets[fid] = frozenset(members)

    shards = data.get("shards")
    if shards is None:
        shards = ["shard-0"]
    if not isinstance(shards, list) or not shards or not all(isinstance(s, str) for s in shards):
        raise InputError("'shards' must be a non-empty list of shard names")
    if len(set(shards)) != len(shards):
        raise InputError("shard names must be unique")

    return {
        "servers": servers,
        "r": r,
        "q": q,
        "fault_sets": fault_sets,
        "shards": list(shards),
    }


# ---------------------------------------------------------------------------
# Solver (exhaustive enumeration -> provable optimality / infeasibility)
# ---------------------------------------------------------------------------

def surviving_replicas(placement: tuple[str, ...], fault_set: frozenset[str]) -> list[str]:
    """Replicas of a placement that survive the loss of a fault set."""
    return [sid for sid in placement if sid not in fault_set]


def enumerate_valid_placements(inst: dict[str, Any]) -> tuple[list[tuple[tuple[str, ...], float]], dict[str, int]]:
    """All single-shard placements satisfying the fault-survivability rule.

    Returns (valid_placements, stats) where each valid placement is a
    (server_id_tuple, cost) pair, and stats records how many combinations
    were examined and rejected so the result is independently checkable.
    """
    servers = inst["servers"]
    r = inst["r"]
    q = inst["q"]
    fault_sets = inst["fault_sets"]

    stats = {
        "servers": len(servers),
        "combinations_examined": 0,
        "rejected_by_fault_rule": 0,
        "accepted": 0,
    }
    valid: list[tuple[tuple[str, ...], float]] = []
    for combo in itertools.combinations(sorted(servers), r):
        stats["combinations_examined"] += 1
        if all(len(surviving_replicas(combo, fs)) >= q for fs in fault_sets.values()):
            stats["accepted"] += 1
            valid.append((combo, sum(servers[s]["cost"] for s in combo)))
        else:
            stats["rejected_by_fault_rule"] += 1
    valid.sort(key=lambda item: (item[1], item[0]))
    return valid, stats


def solve(inst: dict[str, Any]) -> dict[str, Any]:
    """Solve the joint multi-shard placement by exhaustive search.

    Enumerates every fault-valid placement per shard, then explores the
    cartesian product across shards depth-first, pruning branches that
    exceed a server's capacity or cannot beat the incumbent cost. Because
    every non-pruned leaf is a feasible placement and pruned branches are
    provably infeasible or non-improving, the returned optimum is exact.
    """
    servers = inst["servers"]
    shards = inst["shards"]
    r = inst["r"]

    per_shard_valid, per_shard_stats = [], {}
    for shard in shards:
        valid, stats = enumerate_valid_placements(inst)
        per_shard_valid.append(valid)
        per_shard_stats[shard] = stats

    joint_stats = {
        "leaf_assignments_evaluated": 0,
        "branches_pruned_by_capacity": 0,
        "branches_pruned_by_bound": 0,
    }

    best_cost = [float("inf")]
    best_assignment: list[dict[str, tuple[str, ...]]] = [None]
    usage = {sid: 0 for sid in servers}
    chosen: dict[str, tuple[str, ...]] = {}

    def dfs(index: int, cost_so_far: float) -> None:
        if cost_so_far >= best_cost[0]:
            if index > 0:
                joint_stats["branches_pruned_by_bound"] += 1
            return
        if index == len(shards):
            joint_stats["leaf_assignments_evaluated"] += 1
            best_cost[0] = cost_so_far
            best_assignment[0] = dict(chosen)
            return
        shard = shards[index]
        for combo, cost in per_shard_valid[index]:
            if any(usage[s] >= servers[s]["capacity"] for s in combo):
                joint_stats["branches_pruned_by_capacity"] += 1
                continue
            for s in combo:
                usage[s] += 1
            chosen[shard] = combo
            dfs(index + 1, cost_so_far + cost)
            del chosen[shard]
            for s in combo:
                usage[s] -= 1

    dfs(0, 0.0)

    base = {
        "format_version": FORMAT_VERSION,
        "r": inst["r"],
        "q": inst["q"],
        "fault_sets": {fid: sorted(m) for fid, m in sorted(inst["fault_sets"].items())},
        "exhaustive_stats": {
            "per_shard": per_shard_stats,
            "joint": joint_stats,
        },
    }

    if best_assignment[0] is None:
        reasons = []
        for shard, stats in per_shard_stats.items():
            if stats["accepted"] == 0:
                reasons.append(
                    f"shard {shard!r}: no placement of r={inst['r']} distinct servers "
                    f"keeps q={inst['q']} replicas outside every fault set"
                )
        if not reasons:
            reasons.append(
                "fault-valid placements exist per shard, but every joint combination "
                "exceeds at least one server's capacity"
            )
        base["status"] = "INFEASIBLE"
        base["reason"] = "; ".join(reasons)
        return base

    assignment = {shard: list(combo) for shard, combo in best_assignment[0].items()}
    witnesses = {
        shard: {
            fid: surviving_replicas(tuple(combo), inst["fault_sets"][fid])
            for fid in sorted(inst["fault_sets"])
        }
        for shard, combo in best_assignment[0].items()
    }
    base.update(
        status="OPTIMAL",
        total_cost=best_cost[0],
        assignment=assignment,
        witnesses=witnesses,
        capacity_usage={
            sid: usage_count
            for sid, usage_count in sorted(
                _capacity_usage(servers, best_assignment[0]).items()
            )
        },
        optimality="proven by exhaustive enumeration of all fault-valid placements",
    )
    return base


def _capacity_usage(servers: dict[str, dict[str, Any]], assignment: dict[str, tuple[str, ...]]) -> dict[str, int]:
    usage = {sid: 0 for sid in servers}
    for combo in assignment.values():
        for s in combo:
            usage[s] += 1
    return usage


# ---------------------------------------------------------------------------
# Certificate verification
# ---------------------------------------------------------------------------

def verify_certificate(inst: dict[str, Any], cert: dict[str, Any], check_optimality: bool = False) -> list[str]:
    """Return a list of problems with the certificate (empty means valid)."""
    errors: list[str] = []
    servers = inst["servers"]
    r, q = inst["r"], inst["q"]
    fault_sets = inst["fault_sets"]

    status = cert.get("status")
    if status == "OPTIMAL":
        assignment = cert.get("assignment")
        if not isinstance(assignment, dict) or set(assignment) != set(inst["shards"]):
            errors.append("assignment must cover exactly the declared shards")
            return errors
        usage = {sid: 0 for sid in servers}
        total = 0.0
        for shard, combo in assignment.items():
            if not isinstance(combo, list) or len(combo) != r or len(set(combo)) != r:
                errors.append(f"shard {shard!r}: must place r={r} replicas on distinct servers")
                continue
            unknown = [s for s in combo if s not in servers]
            if unknown:
                errors.append(f"shard {shard!r}: unknown servers {unknown!r}")
                continue
            for fid, members in fault_sets.items():
                survivors = surviving_replicas(tuple(combo), members)
                if len(survivors) < q:
                    errors.append(
                        f"shard {shard!r}: fault set {fid!r} leaves only "
                        f"{len(survivors)} surviving replica(s), need q={q}"
                    )
                declared = cert.get("witnesses", {}).get(shard, {}).get(fid)
                if declared is not None and sorted(declared) != sorted(survivors):
                    errors.append(f"shard {shard!r}: witness for fault set {fid!r} is incorrect")
            for s in combo:
                usage[s] += 1
                total += servers[s]["cost"]
        for sid, used in usage.items():
            if used > servers[sid]["capacity"]:
                errors.append(
                    f"server {sid!r}: capacity {servers[sid]['capacity']} exceeded ({used} replicas)"
                )
        if abs(total - cert.get("total_cost", float("nan"))) > 1e-9:
            errors.append(
                f"declared total_cost {cert.get('total_cost')!r} does not match "
                f"recomputed cost {total!r}"
            )
        if check_optimality and not errors:
            recomputed = solve(inst)
            if recomputed["status"] != "OPTIMAL":
                errors.append("re-solving the instance reports INFEASIBLE")
            elif abs(recomputed["total_cost"] - cert["total_cost"]) > 1e-9:
                errors.append(
                    f"not optimal: exhaustive search finds cost "
                    f"{recomputed['total_cost']!r} < {cert['total_cost']!r}"
                )
    elif status == "INFEASIBLE":
        if not isinstance(cert.get("exhaustive_stats"), dict):
            errors.append("INFEASIBLE certificate must carry exhaustive_stats")
        if check_optimality:
            recomputed = solve(inst)
            if recomputed["status"] != "INFEASIBLE":
                errors.append(
                    f"claimed infeasible, but exhaustive search finds a solution "
                    f"with cost {recomputed['total_cost']!r}"
                )
    else:
        errors.append(f"unknown certificate status: {status!r}")
    return errors


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def _read_json(path: str) -> Any:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def _write_json(path: str | None, payload: Any) -> None:
    text = json.dumps(payload, indent=2, sort_keys=False) + "\n"
    if path:
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
    else:
        sys.stdout.write(text)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="replica_placement",
        description="Fault-tolerant replica placement with verifiable certificates.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_solve = sub.add_parser("solve", help="solve an instance and emit a certificate")
    p_solve.add_argument("input", help="problem instance JSON file")
    p_solve.add_argument("-o", "--output", help="certificate output file (default: stdout)")

    p_verify = sub.add_parser("verify", help="verify a certificate against an instance")
    p_verify.add_argument("input", help="problem instance JSON file")
    p_verify.add_argument("certificate", help="certificate JSON file")
    p_verify.add_argument(
        "--check-optimality",
        action="store_true",
        help="re-run the exhaustive search to confirm optimality/infeasibility",
    )

    args = parser.parse_args(argv)

    try:
        inst = load_instance(_read_json(args.input))
    except (InputError, OSError, json.JSONDecodeError) as exc:
        print(f"error: invalid instance: {exc}", file=sys.stderr)
        return 2

    if args.command == "solve":
        cert = solve(inst)
        _write_json(args.output, cert)
        return 0 if cert["status"] == "OPTIMAL" else 1

    try:
        cert = _read_json(args.certificate)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"error: cannot read certificate: {exc}", file=sys.stderr)
        return 2
    errors = verify_certificate(inst, cert, check_optimality=args.check_optimality)
    if errors:
        for err in errors:
            print(f"INVALID: {err}", file=sys.stderr)
        return 1
    print("certificate VALID")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
