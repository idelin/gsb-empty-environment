"""Synthetic end-to-end sample for contribution_bounds.

Generates synthetic records across two shards, merges them under global
per-subject caps, prints per-record clipping evidence, the before/after
contribution audit, merge-state sizes, a withdrawal/correction backfill
demo, and a small-domain L1 enumeration check.

All data is synthetic; the enumeration is a sanity check, not a formal
privacy proof. See README.md for parameter definitions.
"""

import random

from contribution_bounds import (
    BoundConfig,
    ContributionBounder,
    Record,
    build_shard_state,
    enumerate_subject_l1,
    merge_shard_states,
)


def make_synthetic_shards(seed=20260926):
    rng = random.Random(seed)
    subjects = ["alice", "bob", "carol"]
    groups = ["search", "cart", "pay"]
    shards = {"shard-A": [], "shard-B": []}
    n = 0
    for subj in subjects:
        for _ in range(rng.randint(3, 7)):
            shard = rng.choice(list(shards))
            value = round(rng.uniform(-12.0, 12.0), 2)
            shards[shard].append(Record(
                record_id=f"rec-{n:03d}", subject_id=subj,
                group_id=rng.choice(groups), value=value))
            n += 1
    # Cross-shard duplicate: same record_id present in both shards.
    dup = Record(record_id="rec-dup", subject_id="bob",
                 group_id="search", value=4.0)
    shards["shard-A"].append(dup)
    shards["shard-B"].append(dup)
    return shards


def main():
    config = BoundConfig(max_records_per_subject=3, max_groups_per_subject=2,
                         value_lower=-5.0, value_upper=5.0)
    print("=" * 72)
    print("Config:", config)
    print(f"L1 bounds: add/remove={config.l1_bound_add_remove}, "
          f"replace={config.l1_bound_replace}")
    print("=" * 72)

    shards = make_synthetic_shards()
    states = [build_shard_state(recs, sid) for sid, recs in shards.items()]
    report = merge_shard_states(states, config)

    print("\n-- Merge state sizes --")
    for shard_id, size, count in report.shard_state_sizes:
        print(f"  {shard_id}: {count} records, state={size} bytes")
    print(f"  merged: {report.merged_record_count} unique records, "
          f"state={report.merged_state_size} bytes")

    print("\n-- Per-record clipping evidence (merged, global caps) --")
    for e in report.result.evidence:
        flag = "clipped" if e.was_clipped else "       "
        print(f"  {e.record_id:8s} {e.subject_id:6s} {e.shard_id:8s} "
              f"{e.decision:7s} {e.reason:12s} raw={e.raw_value:8.2f} "
              f"clipped={e.clipped_value:6.2f} {flag}")

    print("\n-- Contribution audit (before -> after) --")
    for a in report.result.audit:
        print(f"  {a.subject_id:6s} candidates={a.candidate_count} "
              f"kept={a.kept_count} groups={a.groups_kept} "
              f"raw_sum={a.raw_sum_all:9.2f} clipped_sum={a.clipped_sum_kept:7.2f} "
              f"L1={a.l1_contribution:6.2f} <= {a.l1_bound_add_remove:.1f} "
              f"ok={a.within_bound}")

    print("\n-- Withdrawal + correction backfill demo --")
    bounder = ContributionBounder(config)
    for state in states:
        bounder.add_records(state.records)
    before = bounder.select().kept_ids
    bounder.withdraw_subject("alice")
    after_withdraw = bounder.select().kept_ids
    print(f"  kept before withdrawal : {before}")
    print(f"  kept after alice left  : {after_withdraw}")
    target = after_withdraw[0]
    bounder.correct_record(target, value=999.0)
    corrected = [r for r in bounder.select().kept if r.record_id == target]
    print(f"  corrected {target} value=999.0 -> kept clipped value "
          f"{corrected[0].value if corrected else 'n/a'}")

    print("\n-- Small-domain L1 enumeration (synthetic sanity check) --")
    enum_cfg = BoundConfig(max_records_per_subject=2, max_groups_per_subject=2,
                           value_lower=-2.0, value_upper=3.0)
    out = enumerate_subject_l1(enum_cfg,
                               value_domain=[-5.0, -2.0, 0.0, 3.0, 7.0],
                               group_domain=["g1", "g2", "g3"],
                               shard_ids=["A", "B"])
    for key, val in out.items():
        print(f"  {key}: {val}")
    print("  NOTE: enumeration on synthetic data is a sanity check, "
          "not a formal privacy proof.")


if __name__ == "__main__":
    main()
