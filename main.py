"""keyshift 运行样例：从本地夹具应用重键事件并核验模型。"""
from keyshift import Database, apply_stream, load_fixture, verify_model


def build_sample_db():
    db = Database()
    db.create_table("users", pk_column="user_key",
                    fks={"primary_order": "orders"})
    db.create_table("orders", pk_column="order_key",
                    fks={"owner_key": "users"})
    db.insert("users", "u1", {"name": "alice"})
    db.insert("users", "u2", {"name": "bob"})
    db.insert("users", "u3", {"name": "carol"})
    db.insert("orders", "o1", {"owner_key": "u1"})
    db.insert("orders", "o2", {"owner_key": "u2"})
    db.insert("orders", "o3", {"owner_key": "u3"})
    db.update_fk("users", "u1", "primary_order", "o1")  # u1 <-> o1 环引用
    return db


def show(db, table, key):
    entity = db.get(table, key)
    if entity is None:
        print(f"  get({table!r}, {key!r}) -> None  (旧键不再命中)")
    else:
        print(f"  get({table!r}, {key!r}) -> entity#{entity.entity_id} "
              f"v{entity.version} data={entity.data}")


def main():
    db = build_sample_db()
    print("== 初始状态 ==")
    for key in ("u1", "u2", "u3"):
        show(db, "users", key)
    for key in ("o1", "o2", "o3"):
        show(db, "orders", key)

    events = load_fixture("fixtures/sample_events.json")
    print(f"\n== 应用夹具事件（{len(events)} 条）==")
    snapshots = []
    for result in apply_stream(db, events):
        moves = ", ".join(
            f"{t}.{old}->{new}" for (t, old), new in sorted(result.moves.items()))
        detail = f" [{moves}]" if moves else ""
        extra = f", 级联外键 {result.cascaded} 处" if result.cascaded else ""
        reason = f" ({result.reason})" if result.reason else ""
        print(f"  {result.tx_id}: {result.status}{detail}{extra}{reason}")
        snapshots.append(db.snapshot())

    print("\n== 终态查询 ==")
    for key in ("u1", "u2", "u3", "u30"):
        show(db, "users", key)
    for key in ("o1", "o2", "o3"):
        show(db, "orders", key)

    print("\n== 版本隔离记录 ==")
    for event, reason in db.isolated_events:
        print(f"  {event.tx_id} seq={event.commit_seq} "
              f"{event.old_key!r}->{event.new_key!r}: {reason}")

    print("\n== 全量模型核验（当前库与每笔提交后的快照）==")
    for label, model in [("current", db)] + [
            (f"snapshot@{i + 1}", s) for i, s in enumerate(snapshots)]:
        violations = verify_model(model)
        print(f"  {label}: {len(violations)} violation(s)")
        for violation in violations:
            print(f"    - {violation}")


if __name__ == "__main__":
    main()
