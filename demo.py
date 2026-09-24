"""Runnable sample: apply a local change fixture to a miniature database.

Usage:
    python3 demo.py [path/to/changes.json]

Seeds a users/orders schema, takes a pre-change snapshot, applies the
fixture (simple move, two-entity key swap, three-entity key cycle, a
conflicting transaction that must fail as a whole, a duplicate delivery,
and two late events), then verifies every readable state.
"""

from __future__ import annotations

import os
import sys

from minidb import Database, ForeignKey, apply_events, load_events, verify

DEFAULT_FIXTURE = os.path.join(os.path.dirname(__file__), "fixtures", "changes.json")


def build_db() -> Database:
    db = Database()
    db.create_table("users", primary_key="id")
    db.create_table(
        "orders",
        primary_key="id",
        foreign_keys=[ForeignKey(column="user_id", ref_table="users", ref_column="id")],
    )
    for user_id, name in [
        (1, "alice"), (2, "bob"), (3, "carol"),
        (4, "dave"), (5, "erin"), (6, "frank"),
    ]:
        db.insert("users", {"id": user_id, "name": name})
    for order_id, user_id, item in [
        (100, 1, "book"), (101, 2, "pen"), (102, 2, "lamp"), (103, 4, "mug"),
    ]:
        db.insert("orders", {"id": order_id, "user_id": user_id, "item": item})
    return db


def render(db: Database) -> str:
    lines = []
    for name, table in db.tables.items():
        rows = sorted(table.rows.values(), key=lambda r: r[table.primary_key])
        lines.append(f"  {name}: " + ", ".join(str(row) for row in rows))
    return "\n".join(lines)


def check(label: str, db: Database) -> None:
    problems = verify(db)
    status = "OK" if not problems else f"PROBLEMS: {problems}"
    print(f"  verify({label}): {status}")


def main() -> None:
    fixture_path = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_FIXTURE
    db = build_db()

    print("== initial state ==")
    print(render(db))
    check("initial", db)

    before = db.snapshot()

    print(f"\n== applying fixture {fixture_path} ==")
    report = apply_events(db, load_events(fixture_path))
    for txn_id in report.committed:
        print(f"  committed  {txn_id}")
    for txn_id, reason in report.failed.items():
        print(f"  FAILED     {txn_id}: {reason}")
    for event in report.duplicates:
        print(f"  duplicate  {event.txn_id} seq={event.commit_seq} "
              f"{event.table}.{event.pk} {event.before}->{event.after} (skipped)")
    for event in report.stale:
        print(f"  stale      {event.txn_id} seq={event.commit_seq} "
              f"{event.table}.{event.pk} {event.before}->{event.after} (isolated)")

    print("\n== final state ==")
    print(render(db))
    check("final", db)
    check("pre-change snapshot", before)

    print("\n== spot checks ==")
    print(f"  users id=1  -> {db.get('users', 1)}   (old key must miss)")
    print(f"  users id=10 -> {db.get('users', 10)}   (moved entity)")
    print(f"  orders id=100 -> {db.get('orders', 100)}   (FK followed the entity)")
    print(f"  pre-change snapshot users id=1 -> {before.get('users', 1)}")


if __name__ == "__main__":
    main()
