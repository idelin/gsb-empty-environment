import unittest
from pathlib import Path

from cdc import (
    ChangeParser,
    DELETED,
    FieldDef,
    SchemaRegistry,
    SchemaVersion,
    UnknownFieldError,
)
from cdc.fixtures import load_changes, load_schema_versions

FIXTURES = Path(__file__).resolve().parent.parent / "fixtures"

USERS_V1 = SchemaVersion(
    table="users",
    version=1,
    effective_from=0,
    fields=(
        FieldDef(1, "user_id", "int"),
        FieldDef(2, "user_name", "str"),
        FieldDef(3, "email", "str"),
    ),
)
USERS_V2 = SchemaVersion(
    table="users",
    version=2,
    effective_from=100,
    fields=(
        FieldDef(1, "user_id", "int"),
        FieldDef(2, "display_name", "str"),
        FieldDef(4, "age", "int"),
    ),
)


def make_parser(*versions, capacity=8):
    registry = SchemaRegistry()
    parser = ChangeParser(registry, buffer_capacity=capacity)
    for version in versions:
        parser.register_schema(version)
    return parser


def raw(txn_id, commit_seq, before, after, table="users", pk=None):
    return {
        "txn_id": txn_id,
        "table": table,
        "pk": pk if pk is not None else {"user_id": 1},
        "commit_seq": commit_seq,
        "before": before,
        "after": after,
    }


class VersionSelectionTest(unittest.TestCase):
    def test_version_selected_by_commit_seq(self):
        parser = make_parser(USERS_V1, USERS_V2)
        old = parser.submit(raw("t-old", 99, {}, {"user_id": 1,
                                                  "user_name": "a"}))
        new = parser.submit(raw("t-new", 100, {}, {"user_id": 1,
                                                   "display_name": "a"}))
        self.assertEqual(old.schema_version, 1)
        self.assertEqual(new.schema_version, 2)

    def test_late_old_event_uses_old_field_names(self):
        parser = make_parser(USERS_V1, USERS_V2)
        late = parser.submit(raw(
            "t-late", 40,
            {"user_id": 3, "user_name": "bob", "email": "b@x.com"},
            {"user_id": 3, "user_name": "bobby", "email": "b@x.com"},
        ))
        self.assertEqual(late.schema_version, 1)
        self.assertEqual(late.after[2], "bobby")

    def test_old_name_rejected_after_rename_boundary(self):
        parser = make_parser(USERS_V1, USERS_V2)
        with self.assertRaises(UnknownFieldError):
            parser.submit(raw("t-bad", 150, {},
                              {"user_id": 1, "user_name": "x"}))


class StableIdTest(unittest.TestCase):
    def test_rename_maps_to_same_stable_id(self):
        parser = make_parser(USERS_V1, USERS_V2)
        old = parser.submit(raw("t1", 10, {}, {"user_name": "ann"}))
        new = parser.submit(raw("t2", 110, {}, {"display_name": "ann"}))
        self.assertEqual(old.after, {2: "ann"})
        self.assertEqual(new.after, {2: "ann"})

    def test_dropped_field_not_carried_into_new_version(self):
        parser = make_parser(USERS_V1, USERS_V2)
        with self.assertRaises(UnknownFieldError):
            parser.submit(raw("t", 120, {},
                              {"user_id": 1, "email": "a@b.c"}))


class DeletedVsNullTest(unittest.TestCase):
    def test_absent_after_field_is_deleted_not_null(self):
        parser = make_parser(USERS_V1)
        parsed = parser.submit(raw(
            "t", 5,
            {"user_id": 1, "user_name": "a", "email": "a@b.c"},
            {"user_id": 1, "user_name": "a", "email": None},
        ))
        self.assertIsNone(parsed.after[3])

        parsed = parser.submit(raw(
            "t2", 6,
            {"user_id": 1, "user_name": "a", "email": "a@b.c"},
            {"user_id": 1, "user_name": "a"},
        ))
        self.assertIs(parsed.after[3], DELETED)
        self.assertIsNot(parsed.after[3], None)

    def test_delete_row_marks_all_fields_deleted(self):
        parser = make_parser(USERS_V1)
        parsed = parser.submit(raw(
            "t", 7,
            {"user_id": 1, "user_name": "a", "email": None},
            {},
        ))
        self.assertIs(parsed.after[1], DELETED)
        self.assertIs(parsed.after[2], DELETED)
        self.assertIs(parsed.after[3], DELETED)


class UnknownSchemaBufferTest(unittest.TestCase):
    def test_unknown_schema_is_buffered_then_drained_on_register(self):
        parser = make_parser(USERS_V2)
        self.assertIsNone(parser.submit(raw("t", 40, {},
                                            {"user_name": "x"})))
        self.assertEqual(len(parser.buffer), 1)

        drained = parser.register_schema(USERS_V1)
        self.assertEqual(len(drained), 1)
        self.assertEqual(drained[0].schema_version, 1)
        self.assertEqual(len(parser.buffer), 0)

    def test_unresolvable_events_stay_buffered_after_drain(self):
        parser = make_parser()
        parser.submit(raw("t", 5, {}, {"user_name": "x"}))
        parser.submit(raw("t2", 5, {}, {"user_name": "y"}, table="orders"))
        drained = parser.register_schema(USERS_V1)
        self.assertEqual([p.txn_id for p in drained], ["t"])
        self.assertEqual(len(parser.buffer), 1)

    def test_buffer_is_bounded_and_evicts_oldest(self):
        parser = make_parser(capacity=2)
        for i in range(4):
            parser.submit(raw(f"t{i}", i, {}, {}, table="ghost"))
        self.assertEqual(len(parser.buffer), 2)
        self.assertEqual(parser.buffer.evicted, 2)
        remaining = parser.buffer.take_all()
        self.assertEqual([r["txn_id"] for r in remaining], ["t2", "t3"])


class NoGuessingTest(unittest.TestCase):
    def test_unknown_field_raises_instead_of_guessing(self):
        parser = make_parser(USERS_V2)
        with self.assertRaises(UnknownFieldError):
            parser.submit(raw("t", 130, {}, {"nickname": "x"}))

    def test_unknown_table_is_buffered_not_guessed(self):
        parser = make_parser(USERS_V1)
        self.assertIsNone(parser.submit(raw("t", 1, {}, {},
                                            table="orders")))
        self.assertEqual(len(parser.buffer), 1)

    def test_duplicate_version_registration_rejected(self):
        parser = make_parser(USERS_V1)
        with self.assertRaises(ValueError):
            parser.register_schema(USERS_V1)


class FixturesEndToEndTest(unittest.TestCase):
    def test_fixture_scenario(self):
        schemas = load_schema_versions(FIXTURES / "schema_changes.json")
        changes = load_changes(FIXTURES / "changes.json")
        v1 = next(s for s in schemas if s.version == 1)
        v2 = next(s for s in schemas if s.version == 2)

        parser = make_parser(v2)
        first = parser.submit(changes[0])
        self.assertEqual(first.schema_version, 2)
        self.assertIsNone(first.after[2])

        self.assertIsNone(parser.submit(changes[1]))
        self.assertIsNone(parser.submit(changes[2]))

        drained = parser.register_schema(v1)
        self.assertEqual([p.txn_id for p in drained],
                         ["txn-099", "txn-095"])
        by_txn = {p.txn_id: p for p in drained}
        self.assertEqual(by_txn["txn-099"].after[2], "bobby")
        self.assertIs(by_txn["txn-099"].after[3], DELETED)
        self.assertIsNone(by_txn["txn-095"].before[3])
        self.assertEqual(by_txn["txn-095"].after[3], "c@y.com")


if __name__ == "__main__":
    unittest.main()
