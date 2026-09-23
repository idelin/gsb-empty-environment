"""Tests for schema-versioned CDC parsing. Run: python3 -m unittest discover -s tests -v"""

from __future__ import annotations

import unittest
from pathlib import Path

from cdc import (
    ABSENT,
    BoundedBuffer,
    BufferOverflowError,
    ChangeParser,
    FieldDef,
    FieldTypeError,
    RawChangeEvent,
    SchemaRegistry,
    SchemaVersion,
    UnknownFieldError,
    UnknownSchemaError,
)
from cdc.loader import load_events, load_schemas

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"

USERS_V1 = SchemaVersion(
    table="users",
    version=1,
    effective_from=0,
    fields=(
        FieldDef(1, "id", "int"),
        FieldDef(2, "full_name", "str"),
        FieldDef(3, "email", "str"),
        FieldDef(4, "age", "int"),
    ),
)
USERS_V2 = SchemaVersion(
    table="users",
    version=2,
    effective_from=100,
    fields=(
        FieldDef(1, "id", "int"),
        FieldDef(2, "display_name", "str"),
        FieldDef(3, "email", "str"),
    ),
)


def make_registry() -> SchemaRegistry:
    registry = SchemaRegistry()
    registry.register(USERS_V1)
    registry.register(USERS_V2)
    return registry


def make_parser(max_events: int = 8) -> ChangeParser:
    return ChangeParser(make_registry(), BoundedBuffer(max_events=max_events))


def raw(seq: int, after: dict, before: dict | None = None, table: str = "users",
        txid: str = "tx") -> RawChangeEvent:
    return RawChangeEvent(
        txid=txid, table=table, pk={"id": after.get("id", 1)},
        commit_seq=seq, before=before, after=after,
    )


class VersionSelectionTest(unittest.TestCase):
    def test_schema_takes_effect_at_commit_seq(self):
        parser = make_parser()
        old = parser.parse(raw(99, {"id": 1, "full_name": "Ada"}))
        new = parser.parse(raw(100, {"id": 1, "display_name": "Ada"}))
        self.assertEqual(old.schema_version, 1)
        self.assertEqual(new.schema_version, 2)

    def test_late_old_event_uses_old_field_names(self):
        # v2 already registered, but a seq=50 event must still parse as v1.
        parser = make_parser()
        parsed = parser.parse(raw(50, {"id": 2, "full_name": "Grace", "age": None}))
        self.assertEqual(parsed.schema_version, 1)
        self.assertEqual(parsed.after[2], "Grace")
        self.assertIsNone(parsed.after[4])

    def test_old_event_with_new_name_is_rejected(self):
        # A pre-100 event must not be interpreted with v2 names.
        parser = make_parser()
        with self.assertRaises(UnknownFieldError):
            parser.parse(raw(50, {"id": 2, "display_name": "Grace"}))


class RenameStableIdTest(unittest.TestCase):
    def test_rename_maps_to_same_stable_id(self):
        parser = make_parser()
        before_rename = parser.parse(raw(10, {"id": 1, "full_name": "Ada"}))
        after_rename = parser.parse(raw(120, {"id": 1, "display_name": "Ada"}))
        self.assertEqual(before_rename.after[2], "Ada")
        self.assertEqual(after_rename.after[2], "Ada")


class DeletedVsNullTest(unittest.TestCase):
    def test_null_value_is_none_not_absent(self):
        parser = make_parser()
        parsed = parser.parse(raw(10, {"id": 1, "full_name": "Ada", "email": None}))
        self.assertIsNone(parsed.after[3])
        self.assertIsNot(parsed.after[3], ABSENT)

    def test_missing_field_is_absent_not_null(self):
        parser = make_parser()
        parsed = parser.parse(raw(10, {"id": 1, "full_name": "Ada"}))
        self.assertIs(parsed.after[3], ABSENT)
        self.assertIsNotNone(parsed.after[3])

    def test_dropped_field_reported_as_deleted(self):
        parser = make_parser()
        parsed = parser.parse(raw(120, {"id": 1, "display_name": "Ada"}))
        self.assertEqual(parsed.deleted_field_ids, frozenset({4}))
        self.assertNotIn(4, parsed.after)

    def test_dropped_field_in_new_event_is_rejected(self):
        # age was dropped in v2; a seq>=100 event carrying it must not parse.
        parser = make_parser()
        with self.assertRaises(UnknownFieldError):
            parser.parse(raw(120, {"id": 1, "display_name": "Ada", "age": 36}))


class UnknownSchemaBufferTest(unittest.TestCase):
    def test_unknown_schema_is_buffered_then_replayed(self):
        parser = make_parser()
        event = raw(5, {"id": 9, "total": 100}, table="orders", txid="t4")
        self.assertIsNone(parser.submit(event))
        self.assertEqual(len(parser.buffer), 1)

        parser.registry.register(SchemaVersion(
            table="orders", version=1, effective_from=0,
            fields=(FieldDef(1, "id", "int"), FieldDef(2, "total", "int")),
        ))
        replayed = parser.on_schema_registered("orders")
        self.assertEqual(len(replayed), 1)
        self.assertEqual(replayed[0].txid, "t4")
        self.assertEqual(replayed[0].after[2], 100)
        self.assertEqual(len(parser.buffer), 0)

    def test_buffer_is_bounded(self):
        parser = make_parser(max_events=2)
        for i in range(2):
            parser.submit(raw(i, {"id": i}, table="ghost", txid=f"g{i}"))
        with self.assertRaises(BufferOverflowError):
            parser.submit(raw(3, {"id": 3}, table="ghost", txid="g3"))
        self.assertEqual(len(parser.buffer), 2)

    def test_still_unknown_after_replay_is_rebuffered(self):
        parser = make_parser()
        # seq=5 predates every version of "logs" (first version effective at 10).
        parser.submit(raw(5, {"id": 1}, table="logs", txid="L1"))
        parser.registry.register(SchemaVersion(
            table="logs", version=1, effective_from=10,
            fields=(FieldDef(1, "id", "int"),),
        ))
        replayed = parser.on_schema_registered("logs")
        self.assertEqual(replayed, [])
        self.assertEqual(len(parser.buffer), 1)


class NoGuessingTest(unittest.TestCase):
    def test_undefined_field_is_not_guessed(self):
        parser = make_parser()
        with self.assertRaises(UnknownFieldError):
            parser.parse(raw(130, {"id": 3, "display_name": "Alan", "nickname": "al"}))

    def test_wrong_type_is_not_coerced(self):
        parser = make_parser()
        with self.assertRaises(FieldTypeError):
            parser.parse(raw(10, {"id": "not-an-int", "full_name": "Ada"}))

    def test_resolve_without_any_version_raises(self):
        registry = SchemaRegistry()
        with self.assertRaises(UnknownSchemaError):
            registry.resolve("users", 1)


class FixtureTest(unittest.TestCase):
    def test_fixtures_load_and_parse(self):
        registry = SchemaRegistry()
        for schema in load_schemas(FIXTURES / "schemas.json"):
            registry.register(schema)
        parser = ChangeParser(registry, BoundedBuffer(max_events=8))
        events = load_events(FIXTURES / "events.json")
        self.assertEqual(len(events), 5)

        parsed_by_txid = {}
        for event in events:
            try:
                parsed = parser.submit(event)
            except UnknownFieldError:
                continue
            if parsed is not None:
                parsed_by_txid[parsed.txid] = parsed

        self.assertEqual(parsed_by_txid["t1"].schema_version, 1)
        self.assertIsNone(parsed_by_txid["t1"].after[3])       # email: explicit null
        self.assertEqual(parsed_by_txid["t2"].schema_version, 2)
        self.assertEqual(parsed_by_txid["t2"].deleted_field_ids, frozenset({4}))
        self.assertEqual(parsed_by_txid["t3"].schema_version, 1)  # late old event
        self.assertIsNone(parsed_by_txid["t3"].after[4])          # age null, not deleted
        self.assertNotIn("t4", parsed_by_txid)                    # orders: buffered
        self.assertNotIn("t5", parsed_by_txid)                    # nickname: rejected
        self.assertEqual(len(parser.buffer), 1)


if __name__ == "__main__":
    unittest.main()
