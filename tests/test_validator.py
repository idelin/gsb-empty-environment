import math
import unittest

from batchval import (
    Batch, ColType, ColumnData, ColumnSpec, DictRegistry, Dictionary, K,
    Schema, Severity, StreamValidator, pack_bitmap, slice_bitmap,
)
from batchval.checker import independent_check
from batchval.mutate import base_batches, base_registry, base_schema, mutations


def run_stream(schema, registry, batches, resume_at=None):
    """Feed batches; optionally snapshot/restore mid-stream."""
    sv = StreamValidator(schema, registry)
    errors = []
    for i, b in enumerate(batches):
        if resume_at is not None and i == resume_at:
            sv = StreamValidator(schema, registry, state=sv.snapshot())
        errors.extend(sv.feed_batch(b))
    return errors


def keys(errors):
    return {e.key() for e in errors}


def make_batches(schema, columns, sizes, dict_version=1):
    """Build batches from full-length column buffers, chunking by sizes."""
    batches = []
    offset = 0
    for bi, size in enumerate(sizes):
        cols = []
        for ci, spec in enumerate(schema.columns):
            values, bitmap = columns[ci]
            cols.append(ColumnData(
                spec=spec, length=size,
                values=list(values[offset:offset + size]),
                bitmap=(slice_bitmap(bitmap, offset, offset + size)
                        if bitmap is not None else None),
                dict_ref=((spec.name, dict_version)
                          if spec.type is ColType.STR else None),
            ))
        batches.append(Batch(bi, size, cols))
        offset += size
    return batches


class TestBaseValid(unittest.TestCase):
    def test_base_table_is_clean(self):
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        errors = run_stream(schema, reg, batches)
        self.assertEqual(errors, [], f"unexpected: {errors}")
        self.assertEqual(independent_check(schema, reg, batches), set())

    def test_unsorted_declared_column_not_checked(self):
        # base qty column contains inversions but is not declared sorted
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        qty_errors = [e for e in run_stream(schema, reg, batches)
                      if e.column == "qty"]
        self.assertEqual(qty_errors, [])


class TestMutations(unittest.TestCase):
    def test_every_single_point_mutation(self):
        schema = base_schema()
        for name, kind, severity, batches in mutations():
            with self.subTest(mutation=name):
                reg = base_registry()
                errors = run_stream(schema, reg, batches)
                if kind is None:  # control mutant
                    self.assertEqual(errors, [], f"control mutated: {errors}")
                    continue
                matched = [e for e in errors if e.kind == kind]
                self.assertTrue(matched, f"{kind} not found in {errors}")
                self.assertTrue(all(e.severity is severity for e in matched))
                # streaming validator and independent checker must agree
                self.assertEqual(keys(errors),
                                 independent_check(schema, reg, batches))


class TestEarliestBadRow(unittest.TestCase):
    def test_only_first_inversion_per_column(self):
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        _id = next(c for c in batches[1].columns if c.spec.name == "id")
        _id.values = [2, 1, 0, 9]  # multiple inversions, first at global row 4
        errors = run_stream(schema, reg, batches)
        sort_errors = [e for e in errors if e.kind == K.SORT and e.column == "id"]
        self.assertEqual(len(sort_errors), 1)
        self.assertEqual(sort_errors[0].row, 4)
        self.assertEqual(sort_errors[0].evidence, (3, 2))


class TestChunkingAndResume(unittest.TestCase):
    """Same logical rows, arbitrary chunkings and pause/resume must yield
    the same error set."""

    def _columns(self):
        full = pack_bitmap([1] * 12)
        return [
            ([0, 1, 2, 3, 4, 0, 6, 7, 8, 9, 10, 11], None),   # id: inversion @5
            ([float(i) for i in range(12)], full),            # score: sorted
            ([0, 0, 1, 1, 1, 2, 99, 2, 2, 3, 3, 3], full),    # city: OOB @6
            ([12 - i for i in range(12)], full),              # qty: unsorted
        ]

    def test_arbitrary_chunking_same_errors(self):
        schema, reg = base_schema(), base_registry()
        expected = None
        for sizes in ([4, 4, 4], [6, 6], [1] * 12, [12], [5, 7], [3, 1, 8]):
            with self.subTest(sizes=sizes):
                batches = make_batches(schema, self._columns(), sizes)
                got = keys(run_stream(schema, reg, batches))
                self.assertEqual(got, independent_check(schema, reg, batches))
                if expected is None:
                    expected = got
                self.assertEqual(got, expected)
        self.assertIn((K.SORT, "id", 5), expected)
        self.assertIn((K.DICT_INDEX, "city", 6), expected)

    def test_pause_resume_same_errors(self):
        schema, reg = base_schema(), base_registry()
        batches = make_batches(schema, self._columns(), [4, 4, 4])
        expected = keys(run_stream(schema, reg, batches))
        for resume_at in (1, 2):
            with self.subTest(resume_at=resume_at):
                got = keys(run_stream(schema, reg, batches, resume_at=resume_at))
                self.assertEqual(got, expected)


class TestBoundedReadsAndIndependence(unittest.TestCase):
    def test_huge_offset_no_unbounded_read_and_no_masking(self):
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        city = next(c for c in batches[0].columns if c.spec.name == "city")
        city.values[1] = 10**12  # absurd offset
        qty = next(c for c in batches[0].columns if c.spec.name == "qty")
        qty.values[0] = "not-an-int"  # independent error in another column
        score = next(c for c in batches[2].columns if c.spec.name == "score")
        score.values[3] = 0.0  # independent cross-batch inversion
        errors = run_stream(schema, reg, batches)
        kinds = {(e.kind, e.column) for e in errors}
        self.assertIn((K.DICT_INDEX, "city"), kinds)
        self.assertIn((K.TYPE, "qty"), kinds)
        self.assertIn((K.SORT, "score"), kinds)
        ev = next(e for e in errors if e.kind == K.DICT_INDEX).evidence
        self.assertLessEqual(len(ev[1]), 5)  # minimal bounded slice
        self.assertIn(10**12, ev[1])

    def test_one_column_short_by_one_row(self):
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        city = next(c for c in batches[2].columns if c.spec.name == "city")
        city.values = city.values[:-1]  # exactly one row short
        errors = run_stream(schema, reg, batches)
        length = [e for e in errors if e.kind == K.LENGTH]
        self.assertEqual(len(length), 1)
        self.assertEqual(length[0].column, "city")
        # other columns of the same batch are unaffected
        self.assertFalse([e for e in errors
                          if e.kind == K.LENGTH and e.column != "city"])


class TestNullVersusNaN(unittest.TestCase):
    def _schema(self):
        return Schema(columns=(
            ColumnSpec("f", ColType.FLOAT, nullable=True, sorted_asc=False),
        ))

    def test_nan_is_not_null_and_null_is_not_nan(self):
        schema = self._schema()
        reg = DictRegistry()
        bitmap = pack_bitmap([1, 1, 0, 0])
        values = [math.nan, None, None, math.nan]
        batch = Batch(0, 4, [ColumnData(schema.columns[0], 4, values, bitmap)])
        errors = run_stream(schema, reg, [batch])
        # only row 1 is an error: valid bit set but slot is null.
        # NaN with valid bit (row 0) and anything under a null bit are fine.
        self.assertEqual(keys(errors), {(K.NULL, "f", 1)})


class TestExactIntegers(unittest.TestCase):
    def test_big_integers_compared_exactly(self):
        schema = Schema(columns=(
            ColumnSpec("n", ColType.INT, nullable=False, sorted_asc=True),))
        reg = DictRegistry()
        big = 2**63 + 7
        good = Batch(0, 2, [ColumnData(schema.columns[0], 2, [big, big + 1])])
        self.assertEqual(run_stream(schema, reg, [good]), [])
        bad = Batch(0, 2, [ColumnData(schema.columns[0], 2, [big + 1, big])])
        errs = run_stream(schema, reg, [bad])
        self.assertEqual(keys(errs), {(K.SORT, "n", 1)})

    def test_bool_rejected_in_int_column(self):
        schema = Schema(columns=(
            ColumnSpec("n", ColType.INT, nullable=False),))
        batch = Batch(0, 1, [ColumnData(schema.columns[0], 1, [True])])
        errs = run_stream(schema, DictRegistry(), [batch])
        self.assertEqual(keys(errs), {(K.TYPE, "n", 0)})


class TestSharedBuffers(unittest.TestCase):
    def _two_int_batches(self, share, declare):
        schema = Schema(columns=(
            ColumnSpec("a", ColType.INT, nullable=False),
            ColumnSpec("b", ColType.INT, nullable=False),))
        buf = [1, 2, 3]
        a = ColumnData(schema.columns[0], 3, buf)
        b = ColumnData(schema.columns[1], 3, buf if share else list(buf))
        batch = Batch(0, 3, [a, b],
                      shared_buffers={"buf0": ("a", "b")} if declare else {})
        return schema, batch

    def test_declared_and_shared_is_ok(self):
        schema, batch = self._two_int_batches(share=True, declare=True)
        self.assertEqual(run_stream(schema, DictRegistry(), [batch]), [])

    def test_shared_but_undeclared_is_corrupt(self):
        schema, batch = self._two_int_batches(share=True, declare=False)
        errs = run_stream(schema, DictRegistry(), [batch])
        self.assertEqual(keys(errs), {(K.SHARED, None, None)})

    def test_declared_but_not_shared_is_corrupt(self):
        schema, batch = self._two_int_batches(share=False, declare=True)
        errs = run_stream(schema, DictRegistry(), [batch])
        self.assertEqual(keys(errs), {(K.SHARED, None, None)})


class TestDictionaryVersions(unittest.TestCase):
    def test_sorted_str_cross_batch_inversion(self):
        schema, reg, batches = base_schema(), base_registry(), base_batches()
        city = next(c for c in batches[2].columns if c.spec.name == "city")
        city.values[0] = 0  # "bj" < previous batch's last key "sh"
        errs = run_stream(schema, reg, batches)
        self.assertIn((K.SORT, "city", 8), keys(errs))

    def test_missing_vs_corrupt_severity(self):
        schema = base_schema()
        for name, kind, severity, batches in mutations():
            if kind not in (K.BUFFER, K.DICT_REF, K.DICT_INDEX):
                continue
            with self.subTest(mutation=name):
                errs = run_stream(schema, base_registry(), batches)
                e = next(x for x in errs if x.kind == kind)
                self.assertIs(e.severity, severity)


if __name__ == "__main__":
    unittest.main()
