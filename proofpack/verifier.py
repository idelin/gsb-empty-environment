"""Verifier: replays a proof package and cross-checks it.

Checks performed (each reported individually):
  ids_and_versions      stable IDs + versions on package, rule, facts, steps
  leaf_integrity        every revealed record hashes to its committed leaf
  no_duplicates         contribution indices/rids unique and ordered
  range_contiguity      contributions fill one contiguous index run, edges
                        line up with boundary records and dataset ends
  ordering_and_rule     revealed keys non-decreasing; contributions inside
                        [lo, hi]; boundary records strictly outside
  root_reconstruction   covering digests + revealed leaves rebuild the
                        committed root (no omission, no duplication)
  retractions_valid     each retracted rid was an original contribution,
                        at most once, with matching record content
  arithmetic            sum/count/numerator/denominator/wavg recomputed
                        directly from the effective contribution list;
                        zero denominator must be marked UNDEFINED
  contribution_summary  rolling digest over the effective list recomputed
  step_chain_replay     select/aggregate/retract steps replayed from the
                        genesis hash; final hash equals package_digest
  retraction_vs_previous  (when previous package supplied) incremental
                        deltas applied to the previous aggregates equal the
                        new directly-recomputed aggregates
"""
from __future__ import annotations

from .commitments import leaf_hash, reconstruct_root
from .model import FilterRule, Record, aggregate, contribution_summary
from .prover import GENESIS_HASH, step_hash

_NUMERIC_FIELDS = ("sum", "count", "wavg_numerator", "wavg_denominator")


def verify_package(pkg: dict, expected_root: str | None = None,
                   previous_package: dict | None = None) -> dict:
    checks: list[dict] = []

    def check(cid: str, ok: bool, detail: str = "") -> None:
        checks.append({"check": cid, "ok": bool(ok), "detail": detail})

    evidence = pkg["evidence"]
    contribs = evidence["contributions"]
    bl = evidence["boundary_left"]
    br = evidence["boundary_right"]
    leaf_count = pkg["dataset"]["leaf_count"]

    # 1. stable IDs and versions everywhere
    try:
        ids_ok = (
            isinstance(pkg["package_id"], str) and pkg["package_id"]
            and isinstance(pkg["version"], int) and pkg["version"] >= 1
            and isinstance(pkg["rule"]["rule_id"], str)
            and isinstance(pkg["rule"]["version"], int)
            and isinstance(pkg["dataset"]["dataset_id"], str)
            and len(pkg["steps"]) >= 2
            and all(isinstance(s.get("step_id"), str) and s.get("step_id")
                    and isinstance(s.get("version"), int) for s in pkg["steps"])
            and all(isinstance(c["record"].get("rid"), str)
                    and isinstance(c["record"].get("fact_version"), int)
                    for c in contribs)
        )
    except (KeyError, TypeError, AttributeError):
        ids_ok = False
    check("ids_and_versions", ids_ok)

    # 2. revealed records hash to their committed leaves
    revealed = ([bl] if bl else []) + contribs + ([br] if br else [])
    try:
        leaf_ok = all(item["leaf"] == leaf_hash(item["record"]) for item in revealed)
    except (KeyError, TypeError):
        leaf_ok = False
    check("leaf_integrity", leaf_ok)

    # 3. no duplicates
    idxs = [c["index"] for c in contribs]
    rids = [c["record"]["rid"] for c in contribs]
    check("no_duplicates",
          len(set(idxs)) == len(idxs) and len(set(rids)) == len(rids)
          and idxs == sorted(idxs))

    # 4. contributions form one contiguous run; edges line up
    try:
        if idxs:
            contiguous = idxs == list(range(idxs[0], idxs[-1] + 1))
            left_ok = (bl is None and idxs[0] == 0) or \
                      (bl is not None and bl["index"] == idxs[0] - 1)
            right_ok = (br is None and idxs[-1] == leaf_count - 1) or \
                       (br is not None and br["index"] == idxs[-1] + 1)
        else:
            contiguous = True
            if bl is None and br is None:
                left_ok = right_ok = (leaf_count == 0)
            elif bl is None:
                left_ok = True
                right_ok = br["index"] == 0
            elif br is None:
                left_ok = bl["index"] == leaf_count - 1
                right_ok = True
            else:
                left_ok = right_ok = br["index"] == bl["index"] + 1
        in_range = all(0 <= i < leaf_count for i in idxs) and \
                   (bl is None or 0 <= bl["index"] < leaf_count) and \
                   (br is None or 0 <= br["index"] < leaf_count)
        check("range_contiguity", contiguous and left_ok and right_ok and in_range)
    except (KeyError, TypeError) as exc:
        check("range_contiguity", False, str(exc))

    # 5. ordering and rule bounds (completeness of the claimed range)
    try:
        rule = FilterRule.from_dict(pkg["rule"])
        seq = ([bl] if bl else []) + contribs + ([br] if br else [])
        keys = [item["record"]["key"] for item in seq]
        ordered = keys == sorted(keys)
        inside = all(rule.lo <= c["record"]["key"] <= rule.hi for c in contribs)
        left_out = bl is None or bl["record"]["key"] < rule.lo
        right_out = br is None or br["record"]["key"] > rule.hi
        check("ordering_and_rule", ordered and inside and left_out and right_out)
    except (KeyError, TypeError) as exc:
        check("ordering_and_rule", False, str(exc))

    # 6. root reconstruction from covering digests + revealed leaves
    try:
        items = list(evidence["segments"]) + [
            {"index": item["index"], "height": 0, "hash": item["leaf"]}
            for item in revealed
        ]
        root = reconstruct_root(items, pkg["dataset"]["padded_size"])
        root_ok = root == pkg["dataset"]["root"]
        if expected_root is not None:
            root_ok = root_ok and root == expected_root
        check("root_reconstruction", root_ok, "" if root_ok else "root mismatch")
    except (ValueError, KeyError, TypeError) as exc:
        check("root_reconstruction", False, str(exc))

    # 7. retractions reference real original contributions exactly once
    try:
        retractions = pkg.get("retractions", [])
        orig_by_rid = {}
        for c in contribs:
            orig_by_rid.setdefault(c["record"]["rid"], c["record"])
        seen: set[str] = set()
        retr_ok = True
        for r in retractions:
            if r["rid"] in seen or r["rid"] not in orig_by_rid:
                retr_ok = False
            elif r["record"] != orig_by_rid[r["rid"]]:
                retr_ok = False
            seen.add(r["rid"])
        check("retractions_valid", retr_ok)
    except (KeyError, TypeError) as exc:
        check("retractions_valid", False, str(exc))
        retractions = []
        seen = set()

    # 8. arithmetic: direct recomputation over the effective list
    effective = [c for c in contribs if c["record"]["rid"] not in seen]
    eff_records = [Record.from_fact(c["record"]) for c in effective]
    try:
        agg_ok = aggregate(eff_records) == pkg["aggregates"]
        check("arithmetic", agg_ok,
              "" if agg_ok else "direct recomputation mismatch")
    except (KeyError, TypeError) as exc:
        check("arithmetic", False, str(exc))

    # 9. contribution list summary over the effective list
    try:
        check("contribution_summary",
              contribution_summary([c["leaf"] for c in effective])
              == pkg["contribution_summary"])
    except (KeyError, TypeError) as exc:
        check("contribution_summary", False, str(exc))

    # 10. replay the hash-chained steps from genesis
    try:
        steps = pkg["steps"]
        replay_ok = True
        detail = ""

        s1 = steps[0]
        orig_summary = contribution_summary([c["leaf"] for c in contribs])
        body1 = {k: s1[k] for k in
                 ("step_id", "version", "kind", "rule",
                  "contribution_rids", "contribution_summary")}
        replay_ok &= s1["kind"] == "select"
        replay_ok &= s1["rule"] == pkg["rule"]
        replay_ok &= s1["contribution_rids"] == rids
        replay_ok &= s1["contribution_summary"] == orig_summary
        replay_ok &= s1["prev_hash"] == GENESIS_HASH
        replay_ok &= s1["hash"] == step_hash(GENESIS_HASH, body1)
        if not replay_ok:
            detail = "select step replay failed"

        s2 = steps[1]
        orig_aggs = aggregate([Record.from_fact(c["record"]) for c in contribs])
        body2 = {k: s2[k] for k in ("step_id", "version", "kind", "aggregates")}
        s2_ok = s2["kind"] == "aggregate" and s2["aggregates"] == orig_aggs
        s2_ok &= s2["prev_hash"] == s1["hash"]
        s2_ok &= s2["hash"] == step_hash(s1["hash"], body2)
        replay_ok &= s2_ok
        if not s2_ok:
            detail = "aggregate step replay failed"

        run_records = [Record.from_fact(c["record"]) for c in contribs]
        run_leaves = [c["leaf"] for c in contribs]
        prev_hash = s2["hash"]
        for step in steps[2:]:
            rid = step["removed_rid"]
            pos = next((i for i, r in enumerate(run_records) if r.rid == rid), None)
            if pos is None or step["kind"] != "retract":
                replay_ok = False
                detail = f"retract step references unknown rid {rid!r}"
                break
            rec = run_records[pos]
            expected_delta = {
                "sum": -rec.value, "count": -1,
                "wavg_numerator": -rec.value * rec.weight,
                "wavg_denominator": -rec.weight,
            }
            run_records = run_records[:pos] + run_records[pos + 1:]
            run_leaves = run_leaves[:pos] + run_leaves[pos + 1:]
            body = {k: step[k] for k in
                    ("step_id", "version", "kind", "removed_rid",
                     "removed_record", "delta", "updated_aggregates",
                     "updated_summary", "previous_package_digest")}
            step_ok = step["removed_record"] == rec.to_fact()
            step_ok &= step["delta"] == expected_delta
            step_ok &= step["updated_aggregates"] == aggregate(run_records)
            step_ok &= step["updated_summary"] == contribution_summary(run_leaves)
            step_ok &= step["prev_hash"] == prev_hash
            step_ok &= step["hash"] == step_hash(prev_hash, body)
            replay_ok &= step_ok
            if not step_ok:
                detail = f"retract step {step['step_id']} replay failed"
                break
            prev_hash = step["hash"]

        replay_ok &= pkg["package_digest"] == steps[-1]["hash"]
        replay_ok &= pkg["aggregates"] == (
            steps[-1]["updated_aggregates"] if steps[-1]["kind"] == "retract"
            else steps[-1]["aggregates"])
        check("step_chain_replay", replay_ok, detail)
    except (KeyError, TypeError, IndexError) as exc:
        check("step_chain_replay", False, str(exc))

    # 11. incremental retraction cross-checked against the previous package
    if previous_package is not None:
        try:
            retract_steps = [s for s in pkg["steps"] if s["kind"] == "retract"]
            prev_aggs = previous_package["aggregates"]
            link_ok = (retract_steps[0]["previous_package_digest"]
                       == previous_package["package_digest"])
            delta_ok = True
            for field in _NUMERIC_FIELDS:
                applied = prev_aggs[field] + sum(s["delta"][field]
                                                 for s in retract_steps)
                if applied != pkg["aggregates"][field]:
                    delta_ok = False
            prev_rids = {c["record"]["rid"]
                         for c in previous_package["evidence"]["contributions"]}
            member_ok = all(s["removed_rid"] in prev_rids for s in retract_steps)
            check("retraction_vs_previous", link_ok and delta_ok and member_ok)
        except (KeyError, TypeError, IndexError) as exc:
            check("retraction_vs_previous", False, str(exc))

    return {"ok": all(c["ok"] for c in checks), "checks": checks}
