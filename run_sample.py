"""End-to-end sample: load local fixtures, process CDC events, hot reload
policy with atomic migration, resolve deletes historically, and run
synthetic reference-graph replay validation."""

from __future__ import annotations

import json

from cdc_pseudonym import Engine, Reference, load_fixture


def main() -> None:
    secret, policies, events = load_fixture("fixtures/sample_stream.json")
    (seq_v1, policy_v1), (seq_v2, policy_v2) = policies

    engine = Engine(policy_v1, secret, effective_seq=seq_v1)
    print(f"== policy v{policy_v1.version} active (strict) ==")

    pre_reload = [e for e in events if e.commit_seq < seq_v2]
    post_reload = [e for e in events if e.commit_seq >= seq_v2]
    late = [e for e in pre_reload if e.commit_seq == 6]
    pre_reload = [e for e in pre_reload if e.commit_seq != 6]

    for event in pre_reload:
        engine.process(event)
    print(f"processed {len(pre_reload)} events under v{policy_v1.version}; "
          f"mappings: {len(engine._stores[policy_v1.version])}")

    ok = engine.reload_policy(policy_v2, effective_seq=seq_v2)
    print(f"\n== hot reload to v{policy_v2.version}: ok={ok} "
          f"(atomic migration of {len(engine._stores[policy_v2.version])} mappings) ==")

    for event in post_reload:
        engine.process(event)
    print(f"processed {len(post_reload)} events under v{policy_v2.version}")

    # Late-arriving historical delete (commit_seq 6 < reload seq 100):
    # resolved with the policy/key in effect at that time, not the current key.
    for event in late:
        engine.process(event)
    historical_delete = engine.output[-1]
    original_insert = engine.output[0]
    print(f"\n== historical delete (seq {historical_delete['commit_seq']}) ==")
    print(f"delete resolved under policy v{historical_delete['policy_version']}: "
          f"{historical_delete['before']['id']}")
    print(f"matches original v1 insert pseudonym: "
          f"{historical_delete['before']['id'] == original_insert['after']['id']}")

    print("\n== masked output ==")
    for out in engine.output:
        print(json.dumps(out, ensure_ascii=False, sort_keys=True))

    print("\n== error log (no raw values) ==")
    for record in engine.logger.records:
        print(json.dumps(record, ensure_ascii=False, sort_keys=True))

    # Synthetic reference-graph replay validation.
    references = [Reference("orders", "customer_id", "customer", "id")]
    report = engine.commit_replay(events, references)
    print(f"\n== replay validation == ok={report.ok}, "
          f"events={report.checked_events}, refs={report.checked_references}, "
          f"output_version={engine.output_version}")

    # Tampered stream (dangling FK) must fail and keep the old output version.
    bad = [e for e in events if not (e.table == "customer" and e.pk.get("id") == "C-002")]
    before_version = engine.output_version
    bad_report = engine.commit_replay(bad, references)
    print(f"== tampered replay == ok={bad_report.ok}, failures={bad_report.failures}")
    print(f"output_version kept: {engine.output_version == before_version} "
          f"(v{engine.output_version})")


if __name__ == "__main__":
    main()
