#!/usr/bin/env python3
"""Sample run: mask local CDC fixtures, hot-update policy, replay-validate."""
import json
import os

from cdc_masking import (
    ChangeEvent,
    MappingRegistry,
    MaskingEngine,
    MaskingError,
    PolicyError,
    PolicyStore,
    VersionedOutput,
    check_invisible_non_interference,
    load_events,
    load_policy,
    load_schema,
    validate_replay,
)

FIXTURES = os.path.join(os.path.dirname(os.path.abspath(__file__)), "fixtures")


def show(title, masked):
    print(f"  {title}")
    print("   ", json.dumps(masked, ensure_ascii=False, sort_keys=True))


def main():
    schemas = load_schema(os.path.join(FIXTURES, "schema.json"))
    store = PolicyStore(load_policy(os.path.join(FIXTURES, "policy_v1.json")),
                        schemas, strict=True)
    registry = MappingRegistry()
    engine = MaskingEngine(store, registry)
    events = load_events(os.path.join(FIXTURES, "changes.json"))
    output = VersionedOutput()

    print("== 1. Mask events under policy v1 (commit_seq 1-5) ==")
    masked_v1 = [engine.process(ev) for ev in events if ev.commit_seq < 10]
    for m in masked_v1:
        show(f"{m.op} {m.table} tx={m.tx_id} seq={m.commit_seq} v{m.policy_version}",
             {"pk": m.pk, "before": m.before, "after": m.after})
    insert_u1 = masked_v1[0].after["id"]
    delete_u1 = masked_v1[-1].pk["id"]
    print(f"  delete locatable by old key: {insert_u1 == delete_u1} ({delete_u1})")
    report = validate_replay(masked_v1, store, registry)
    output.publish(1, masked_v1, report)
    print(f"  replay v1 ok={report.ok} -> published output version {output.current_version}")

    print("\n== 2. Schema evolution: users.phone (sensitive) appears ==")
    schemas.evolve("users", {"phone": True})
    try:
        store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v2.json")),
                         registry, valid_from_seq=10)
    except PolicyError as exc:
        print(f"  hot update v2 rejected: {exc}")
    print(f"  active version still: v{store.active.version}")

    print("\n== 3. Strict mode rejects event with unmapped sensitive field ==")
    ev10 = next(ev for ev in events if ev.commit_seq == 10)
    try:
        engine.process(ev10)
    except MaskingError as exc:
        print(f"  event rejected: {exc}")
    print(f"  error log: {json.dumps(engine.error_log, ensure_ascii=False)}")
    leaked = "+86-13800000000" in json.dumps(engine.error_log)
    print(f"  raw phone leaked into error path: {leaked}")

    print("\n== 4. Hot update v3 (phone policy added), atomic migration ==")
    store.hot_update(load_policy(os.path.join(FIXTURES, "policy_v3.json")),
                     registry, valid_from_seq=10)
    print(f"  active version: v{store.active.version}, "
          f"migrated mappings: {sorted(registry.versions())}")
    masked_v3 = [engine.process(ev) for ev in events if ev.commit_seq >= 10]
    for m in masked_v3:
        show(f"{m.op} {m.table} tx={m.tx_id} seq={m.commit_seq} v{m.policy_version}",
             {"pk": m.pk, "before": m.before, "after": m.after})

    print("\n== 5. Late delete with only old keys resolves with event-time policy ==")
    late_delete = ChangeEvent(tx_id="tx-9", table="users", op="delete",
                              pk={"id": "u1"}, before={"id": "u1"},
                              after=None, commit_seq=5)
    masked_late = engine.process(late_delete)
    print(f"  processed after v3 is active, resolved as v{masked_late.policy_version}: "
          f"{masked_late.pk['id']}")
    print(f"  matches original v1 delete token: {masked_late.pk['id'] == delete_u1}")

    print("\n== 6. Replay validation across versions ==")
    all_masked = masked_v1 + masked_v3
    report = validate_replay(all_masked, store, registry)
    print(f"  cross-version replay: ok={report.ok} versions={report.versions} "
          f"nodes={report.nodes} edges={report.edges}")
    print(f"  invisible-field non-interference: "
          f"{check_invisible_non_interference(all_masked, store, registry)}")
    output.publish(3, all_masked, report)
    print(f"  published output version: {output.current_version}")

    print("\n== 7. Failed validation keeps old output version ==")
    tampered = [engine.process(ev) for ev in events if ev.commit_seq >= 10]
    bad_row = dict(tampered[1].after)
    bad_row["user_id"] = "v3-forgedtoken00000000"
    tampered[1] = type(tampered[1])(**{**tampered[1].__dict__, "after": bad_row})
    bad_report = validate_replay(tampered, store, registry)
    print(f"  tampered replay: ok={bad_report.ok} failures={list(bad_report.failures)}")
    accepted = output.publish(4, tampered, bad_report)
    print(f"  publish accepted: {accepted}, output version kept: {output.current_version}")


if __name__ == "__main__":
    main()
