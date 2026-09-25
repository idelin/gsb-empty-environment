"""unittest suite for the streaming batch validator."""
from __future__ import annotations

import pickle
import random
import unittest

from batchval import (Batch, BufferRef, Column, CType, Severity, TableSchema,
                      Validator, earliest_bad_row, oracle_check, validate)
from batchval.mutate import MUTATIONS, apply_mutation, make_env


def _err_keys(errors) -> list[tuple]:
    return sorted(e.key() if hasattr(e, "key") else
                  (e[0], e[1].value, e[2], e[3], e[4]) for e in errors)


def _oracle_keys(env) -> list[tuple]:
    return sorted((k, s.value, c, b, r) for k, s, c, b, r in
                  oracle_check(env.schema, env.batches, env.dictionaries, env.buffers))


def _run(env) -> list[tuple]:
    return _err_keys(validate(env.schema, env.batches, env.dictionaries, env.buffers))


def _run_with_pauses(env, suspend: bool) -> list[tuple]:
    v = Validator(env.schema, env.dictionaries, env.buffers)
    for batch in env.batches:
        v.feed(batch)
        if suspend:  # serialize/deserialize full state between every batch
            blob = pickle.dumps(v.snapshot())
            v = Validator.restore(env.schema, pickle.loads(blob),
                                  env.dictionaries, env.buffers)
    return _err_keys(v.errors())


def _run_chunked(env, rng) -> list[tuple]:
    v = Validator(env.schema, env.dictionaries, env.buffers)
    i = 0
    while i < len(env.batches):
        for _ in range(rng.randint(1, 2)):
            if i < len(env.batches):
                v.feed(env.batches[i])
                i += 1
        blob = pickle.dumps(v.snapshot())  # pause/resume at every chunk boundary
        v = Validator.restore(env.schema, pickle.loads(blob),
                              env.dictionaries, env.buffers)
    return _err_keys(v.errors())


def _schema_3(sorted_id=True):
    order = {"id": "asc"} if sorted_id else {}
    return TableSchema(
        columns=[("id", CType.INT64), ("v", CType.FLOAT64), ("lab", CType.UTF8)],
        sorted=order)


def _validity(bits: list[int]) -> bytes:
    out = bytearray((len(bits) + 7) // 8)
    for r, b in enumerate(bits):
        out[r >> 3] |= b << (r & 7)
    return bytes(out)


class CombinedCheckTests(unittest.TestCase):
    def test_cross_batch_inversion_is_detected(self):
        dicts = {"d": {1: ["x"]}}
        b0 = Batch(3, [Column("id", CType.INT64, data=[1, 2, 3]),
                       Column("v", CType.FLOAT64, data=[0.1, 0.2, 0.3]),
                       Column("lab", CType.UTF8, data=[0, 0, 0],
                              dict_id="d", dict_version=1)])
        b1 = Batch(2, [Column("id", CType.INT64, data=[2, 9]),  # 2 < 3 across boundary
                       Column("v", CType.FLOAT64, data=[0.4, 0.5]),
                       Column("lab", CType.UTF8, data=[0, 0],
                              dict_id="d", dict_version=1)])
        errors = validate(_schema_3(), [b0, b1], dicts)
        order_errors = [e for e in errors if e.kind == "order"]
        self.assertEqual(len(order_errors), 1)
        self.assertEqual((order_errors[0].batch, order_errors[0].row), (1, 3))
        self.assertEqual(order_errors[0].evidence, ((2, "3"), (3, "2")))
        self.assertEqual(earliest_bad_row(errors), 3)

    def test_column_short_one_row_is_length_error(self):
        batch = Batch(3, [
            Column("id", CType.INT64, data=[1, 2, 3]),
            Column("v", CType.FLOAT64, data=[0.1, 0.2], validity=_validity([1, 1])),
            Column("lab", CType.UTF8, data=[0, 0, 0], dict_id="d", dict_version=1),
        ])
        errors = validate(_schema_3(), [batch], {"d": {1: ["x"]}})
        kinds = [(e.kind, e.column, e.severity) for e in errors]
        self.assertIn(("length", "v", Severity.CORRUPT), kinds)
        self.assertEqual([e for e in errors if e.kind == "order"], [])

    def test_undeclared_sort_is_not_enforced(self):
        batch = Batch(4, [
            Column("id", CType.INT64, data=[4, 3, 2, 1]),
            Column("v", CType.FLOAT64, data=[0.4, 0.3, 0.2, 0.1]),
            Column("lab", CType.UTF8, data=[0, 0, 0, 0], dict_id="d", dict_version=1),
        ])
        errors = validate(_schema_3(sorted_id=False), [batch], {"d": {1: ["x"]}})
        self.assertEqual([e for e in errors if e.kind == "order"], [])

    def test_null_and_nan_are_distinct_in_order(self):
        s = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64)],
                        sorted={"v": "asc"})
        # NULL first, then number, then NaN: valid total order
        b0 = Batch(3, [Column("id", CType.INT64, data=[1, 2, 3]),
                       Column("v", CType.FLOAT64, data=[0.0, 1.0, float("nan")],
                              validity=_validity([0, 1, 1]))])
        self.assertEqual(validate(s, [b0]), [])
        # NaN then number is an inversion; NaN itself is valid (not null)
        b1 = Batch(2, [Column("id", CType.INT64, data=[1, 2]),
                       Column("v", CType.FLOAT64, data=[float("nan"), 1.0],
                              validity=_validity([1, 1]))])
        errors = validate(s, [b1])
        self.assertEqual([e.kind for e in errors], ["order"])
        self.assertEqual(errors[0].row, 1)

    def test_exact_integer_comparison_and_range(self):
        big_a, big_b = 2 ** 53 + 1, 2 ** 53 + 2  # indistinguishable as float64
        batch = Batch(2, [Column("id", CType.INT64, data=[big_a, big_b]),
                          Column("v", CType.FLOAT64, data=[1.0, 2.0])])
        s = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64)],
                        sorted={"id": "asc"})
        self.assertEqual(validate(s, [batch]), [])
        batch.columns[0].data = [big_b, big_a]
        self.assertEqual([e.kind for e in validate(s, [batch])], ["order"])
        batch.columns[0].data = [2 ** 63, 0]  # first value out of int64 range
        errors = validate(s, [batch])
        self.assertIn("type", {e.kind for e in errors})

    def test_missing_dict_version_is_not_corruption(self):
        batch = Batch(2, [
            Column("id", CType.INT64, data=[1, 2]),
            Column("v", CType.FLOAT64, data=[1.0, 2.0]),
            Column("lab", CType.UTF8, data=[0, 1], dict_id="d", dict_version=7),
        ])
        errors = validate(_schema_3(), [batch], {"d": {1: ["x"]}})
        self.assertEqual(len(errors), 1)
        self.assertEqual((errors[0].kind, errors[0].severity),
                         ("dict_version", Severity.MISSING))

    def test_bad_offset_is_bounded_and_does_not_mask_other_errors(self):
        dicts = {"d": {1: ["x"]}}
        batch = Batch(3, [
            Column("id", CType.INT64, data=[1, 2, 3]),
            Column("v", CType.FLOAT64, data=[0.1, 0.2], validity=_validity([1, 1])),
            Column("lab", CType.UTF8, data=[0, 99, 0],
                   dict_id="d", dict_version=1),
            Column("p", CType.UTF8, buffer=BufferRef("buf", 10 ** 12, 3)),
        ])
        schema = TableSchema(columns=[("id", CType.INT64), (("v"), CType.FLOAT64),
                                      ("lab", CType.UTF8), ("p", CType.UTF8)])
        errors = validate(schema, [batch], dicts, {"buf": ["a", "b", "c"]})
        kinds = {(e.kind, e.severity) for e in errors}
        self.assertIn(("buffer", Severity.CORRUPT), kinds)
        self.assertIn(("length", Severity.CORRUPT), kinds)  # clamped read is empty
        self.assertIn(("dict_index", Severity.CORRUPT), kinds)
        for e in errors:  # evidence stays small
            self.assertLess(len(str(e.evidence)), 80)

    def test_missing_buffer_is_missing_not_corrupt(self):
        batch = Batch(1, [
            Column("id", CType.INT64, data=[1]),
            Column("v", CType.FLOAT64, data=[1.0]),
            Column("lab", CType.UTF8, data=[0], dict_id="d", dict_version=1),
            Column("p", CType.UTF8, buffer=BufferRef("absent", 0, 1)),
        ])
        schema = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64),
                                      ("lab", CType.UTF8), ("p", CType.UTF8)])
        errors = validate(schema, [batch], {"d": {1: ["x"]}}, buffers={})
        self.assertEqual([(e.kind, e.severity) for e in errors],
                         [("buffer", Severity.MISSING)])

    def test_bitmap_size_error(self):
        batch = Batch(9, [
            Column("id", CType.INT64, data=list(range(9))),
            Column("v", CType.FLOAT64, data=[float(i) for i in range(9)],
                   validity=b"\xff"),  # needs 2 bytes for 9 rows
        ])
        s = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64)])
        errors = validate(s, [batch])
        self.assertEqual([(e.kind, e.column) for e in errors], [("bitmap", "v")])

    def test_empty_batch_is_valid(self):
        batch = Batch(0, [Column("id", CType.INT64, data=[]),
                          Column("v", CType.FLOAT64, data=[], validity=b"")])
        s = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64)],
                        sorted={"id": "asc"})
        self.assertEqual(validate(s, [batch, batch]), [])


class StreamingTests(unittest.TestCase):
    def test_generator_is_consumed_lazily_and_planted_error_found(self):
        def gen():
            for b in range(500):
                n = 4
                ids = [b * 10 + r for r in range(n)]
                if b == 499:
                    ids[3] = ids[2] - 1  # inversion in the final batch
                yield Batch(n, [Column("id", CType.INT64, data=ids),
                                Column("v", CType.FLOAT64,
                                       data=[0.0] * n, validity=b"\x0f")])

        s = TableSchema(columns=[("id", CType.INT64), ("v", CType.FLOAT64)],
                        sorted={"id": "asc"})
        validator = Validator(s)
        for batch in gen():
            validator.feed(batch)
        errors = validator.errors()
        self.assertEqual([e.kind for e in errors], ["order"])
        self.assertEqual(errors[0].row, 1999)
        # long-lived state does not grow with rows
        self.assertEqual(set(validator._sort_state), {"id"})

    def test_chunking_pause_resume_give_identical_error_sets(self):
        rng = random.Random(42)
        for _ in range(7):
            env = make_env(rng)
            name = rng.choice(list(MUTATIONS))
            menv, expected = apply_mutation(env, name, random.Random(rng.random()))
            one_shot = _run(menv)
            self.assertEqual(_run_with_pauses(menv, suspend=True), one_shot)
            self.assertEqual(_run_with_pauses(menv, suspend=False), one_shot)
            self.assertEqual(_run_chunked(menv, random.Random(7)), one_shot)
            self.assertEqual(_oracle_keys(menv), one_shot)
            self.assertIn(expected, {k[0] for k in one_shot})


class MutationFuzzTests(unittest.TestCase):
    def test_every_single_point_mutation_matches_oracle(self):
        for seed in range(40):
            env = make_env(random.Random(seed))
            self.assertEqual(_run(env), [], f"base table corrupt for seed {seed}")
            self.assertEqual(_oracle_keys(env), [])
            for name in MUTATIONS:
                menv, expected = apply_mutation(env, name, random.Random(seed + 31))
                got = _run(menv)
                self.assertTrue(got, f"{name} produced no errors (seed {seed})")
                self.assertIn(expected, {k[0] for k in got},
                              f"{name} did not surface (seed {seed})")
                self.assertEqual(got, _oracle_keys(menv),
                                 f"{name} mismatch (seed {seed}):\n{got}")
                # pause/resume must report exactly the same error multiset
                self.assertEqual(_run_with_pauses(menv, suspend=True), got)

    def test_missing_mutations_are_severity_missing(self):
        env = make_env(random.Random(1))
        menv, _ = apply_mutation(env, "missing_dict_version", random.Random(2))
        severities = {k[1] for k in _run(menv) if k[0] == "dict_version"}
        self.assertEqual(severities, {"missing"})
        menv, _ = apply_mutation(env, "huge_buffer_offset", random.Random(3))
        buf = [k for k in _run(menv) if k[0] == "buffer"]
        self.assertTrue(buf)
        self.assertEqual({k[1] for k in buf}, {"corrupt"})


if __name__ == "__main__":
    unittest.main()
