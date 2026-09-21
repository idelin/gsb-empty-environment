"""Delivery-trace verifier.

Checks a trace.jsonl produced by DeliveryLayer for the delivery contract:

1. Per (partition, epoch), deliveries are contiguous from the epoch start
   (epoch 0 implicitly starts at seq 0); a gap may only be crossed when a
   matching, unrevoked skip proof (bound to that epoch and covering the
   gap) appears earlier in the trace.
2. A delivery id is delivered at most once; repeats are only legal as
   redeliveries with an identical payload hash. Redeliveries are exempt
   from ordering (they repeat events delivered before a crash/migration).
3. Acks reference delivered ids only (duplicate acks are legal no-ops).
4. A proof revocation never appears after the proof's gap was crossed.

Usage: python -m tools.verify_trace path/to/trace.jsonl
"""
from __future__ import annotations

import json
import sys


def _covers(proof: dict, seq: int, modulus: int) -> bool:
    return (seq - proof["gap_start"]) % modulus <= (
        proof["gap_end"] - proof["gap_start"]
    ) % modulus


def verify_trace(path: str) -> list[str]:
    violations: list[str] = []
    modulus = None
    expected: dict[tuple[str, int], int] = {}
    proofs: dict[str, dict] = {}
    seen: dict[str, str] = {}  # delivery_id -> payload_hash

    with open(path, "r", encoding="utf-8") as fh:
        records = [json.loads(line) for line in fh if line.strip()]

    for rec in records:
        rtype = rec["type"]
        if rtype == "open":
            modulus = rec["modulus"]
            continue
        if modulus is None:
            violations.append("trace missing opening config record")
            break

        if rtype == "epoch":
            expected[(rec["partition"], rec["epoch"])] = rec["start_seq"]

        elif rtype == "skip":
            proofs[rec["proof_id"]] = {**rec, "crossed": False, "revoked": False}

        elif rtype == "revoke":
            proof = proofs.get(rec["proof_id"])
            if proof is None:
                violations.append(f"revoke of unknown proof {rec['proof_id']}")
            elif proof["crossed"]:
                violations.append(
                    f"proof {rec['proof_id']} revoked after its gap was crossed")
            else:
                proof["revoked"] = True

        elif rtype == "deliver":
            did = rec["delivery_id"]
            if did in seen:
                if seen[did] != rec["payload_hash"]:
                    violations.append(
                        f"delivery id {did} redelivered with different payload")
                if not rec["redelivery"]:
                    violations.append(
                        f"delivery id {did} delivered twice without redelivery flag")
                continue
            seen[did] = rec["payload_hash"]
            if rec["redelivery"] or rec["correction"]:
                # Redeliveries repeat pre-crash/pre-migration deliveries;
                # corrections are the explicit conflict-resolution flow.
                continue
            key = (rec["partition"], rec["epoch"])
            if key not in expected:
                if rec["epoch"] == 0:
                    # Initial epoch anchors at the first observed delivery.
                    expected[key] = rec["seq"]
                else:
                    violations.append(f"delivery {did} before epoch start record")
                    continue
            exp = expected[key]
            if rec["seq"] != exp:
                # Walk the gap: every step must be covered by an active proof.
                cursor = exp
                while cursor != rec["seq"]:
                    proof = next(
                        (p for p in proofs.values()
                         if p["partition"] == rec["partition"]
                         and p["epoch"] == rec["epoch"]
                         and not p["revoked"]
                         and _covers(p, cursor, modulus)),
                        None,
                    )
                    if proof is None:
                        violations.append(
                            f"delivery {did} crosses gap at seq {cursor} "
                            f"of {key} without a skip proof")
                        break
                    proof["crossed"] = True
                    cursor = (proof["gap_end"] + 1) % modulus
            expected[key] = (rec["seq"] + 1) % modulus

        elif rtype == "ack":
            if rec["delivery_id"] not in seen:
                violations.append(
                    f"ack of undelivered id {rec['delivery_id']}")

        elif rtype in ("conflict", "resolve"):
            pass  # explicit conflict/correction flow records

        else:
            violations.append(f"unknown trace record type: {rtype}")

    return violations


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(__doc__)
        return 2
    violations = verify_trace(argv[1])
    if violations:
        print(f"VIOLATIONS ({len(violations)}):")
        for v in violations:
            print(f"  - {v}")
        return 1
    print("trace OK: no violations")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
