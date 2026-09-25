import json
import math
import os
import random
import shutil
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from dictmerge import (
    FLOAT,
    INT,
    STR,
    Batch,
    CancelController,
    LocalDictionary,
    MergeCancelled,
    MergeConfig,
    MergeError,
    Merger,
    Store,
    TermRetractedError,
    VersionMismatchError,
    decode_rows,
)


def rows_equal(a, b):
    """Row-list equality where NaN matches NaN."""
    if len(a) != len(b):
        return False
    for ra, rb in zip(a, b):
        if len(ra) != len(rb):
            return False
        for x, y in zip(ra, rb):
            if (
                isinstance(x, float)
                and isinstance(y, float)
                and math.isnan(x)
                and math.isnan(y)
            ):
                continue
            if x != y:
                return False
    return True


def reference_rows(batches):
    """Reference semantics: decode each batch, concatenate."""
    rows = []
    for b in batches:
        rows.extend(decode_rows(b))
    return rows


class TestCaseBase(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="dictmerge-")
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.store = Store(os.path.join(self.dir, "store"))

    def make_store(self, name):
        return Store(os.path.join(self.dir, name))


class TestBasicSemantics(TestCaseBase):
    def test_reversed_dicts_code_zero_not_same_value(self):
        d1 = LocalDictionary()
        d1.add("a")
        d1.add("b")
        d2 = LocalDictionary()
        d2.add("b")
        d2.add("a")
        schema = [("s", STR)]
        b1 = Batch.from_rows(schema, [("a",), ("b",)], {"s": d1})
        b2 = Batch.from_rows(schema, [("b",), ("a",)], {"s": d2})
        # code 0 means 'a' in d1 but 'b' in d2
        self.assertEqual(b1.values["s"][0], 0)
        self.assertEqual(b2.values["s"][0], 0)

        result = Merger(self.store).merge([b1, b2], job_id="j1")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [("a",), ("b",), ("b",), ("a",)])
        self.assertEqual(merged, reference_rows([b1, b2]))

    def test_empty_string_is_not_null(self):
        d = LocalDictionary()
        d.add("")
        schema = [("s", STR)]
        b = Batch.from_rows(schema, [("",), (None,)], {"s": d})
        result = Merger(self.store).merge([b], job_id="j2")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [("",), (None,)])
        self.assertEqual(len(merged[0][0]), 0)
        self.assertIsNone(merged[1][0])

    def test_merge_decode_equals_concatenation(self):
        schema = [("s", STR), ("n", INT), ("f", FLOAT)]
        dicts, batches = [], []
        words = ["x", "y", "z", "w"]
        for k in range(4):
            d = LocalDictionary()
            for w in random.Random(100 + k).sample(words, len(words)):
                d.add(w)
            dicts.append(d)
            rows = [
                (words[(k + i) % 4], i * (k + 1), float(i) + 0.5)
                for i in range(5)
            ]
            rows[2] = (None, None, None)
            batches.append(Batch.from_rows(schema, rows, {"s": d}))
        result = Merger(self.store).merge(batches, job_id="j3")
        merged = list(self.store.iter_rows(result.version))
        self.assertTrue(rows_equal(merged, reference_rows(batches)))

    def test_null_bitmap_preserved(self):
        d = LocalDictionary()
        for w in ["p", "q"]:
            d.add(w)
        schema = [("s", STR), ("n", INT)]
        rows = [("p", 1), (None, 2), ("q", None), (None, None), ("p", 3)]
        b = Batch.from_rows(schema, rows, {"s": d})
        result = Merger(self.store).merge([b], job_id="j4")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [tuple(r) for r in rows])

    def test_nan_distinct_from_null(self):
        schema = [("f", FLOAT)]
        b = Batch.from_rows(schema, [(float("nan"),), (None,), (1.5,)])
        result = Merger(self.store).merge([b], job_id="j5")
        merged = list(self.store.iter_rows(result.version))
        self.assertTrue(math.isnan(merged[0][0]))
        self.assertIsNone(merged[1][0])
        self.assertEqual(merged[2][0], 1.5)
        # NaN is a value, not a null
        self.assertFalse(b.is_null("f", 0))
        self.assertTrue(b.is_null("f", 1))

    def test_int_exact_arithmetic(self):
        big = 2**70 + 12345
        schema = [("n", INT)]
        b = Batch.from_rows(schema, [(big,), (-big,), (0,)])
        result = Merger(self.store).merge([b], job_id="j6")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [(big,), (-big,), (0,)])
        self.assertEqual(merged[0][0] + merged[1][0], 0)


class TestDictionaryFeatures(TestCaseBase):
    def test_same_term_different_codes(self):
        d = LocalDictionary()
        c0 = d.add("x")
        c1 = d.add_alias("x")  # same word, different code
        self.assertNotEqual(c0, c1)
        schema = [("s", STR)]
        b = Batch(schema, 2)
        b.values["s"][0] = c0
        b.values["s"][1] = c1
        b.dicts["s"] = d
        b.dict_versions["s"] = d.version
        result = Merger(self.store).merge([b], job_id="j7")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [("x",), ("x",)])
        # unified to a single global term
        payload = self.store.load_dict(result.version)
        self.assertEqual(payload["columns"]["s"]["terms"], ["x"])

    def test_forced_hash_collision(self):
        d = LocalDictionary(hash_fn=lambda v: 0)  # everything collides
        for w in ["alpha", "beta", "gamma", "delta"]:
            d.add(w)
        self.assertEqual(d.encode("gamma"), 2)
        self.assertEqual(d.decode(3), "delta")
        schema = [("s", STR)]
        rows = [("delta",), ("alpha",), ("gamma",), ("beta",)]
        b = Batch.from_rows(schema, rows, {"s": d})
        result = Merger(self.store).merge([b], job_id="j8")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [tuple(r) for r in rows])

    def test_retraction_and_old_version_decode(self):
        d = LocalDictionary()
        d.add("keep")
        d.add("drop")
        schema = [("s", STR)]
        old_batch = Batch.from_rows(schema, [("drop",), ("keep",)], {"s": d})
        snapshot_v1 = LocalDictionary.from_dict(d.to_dict())

        d.retract("drop")
        with self.assertRaises(TermRetractedError):
            d.encode("drop")
        # old batch must not be decoded against the new dictionary version
        with self.assertRaises(VersionMismatchError):
            decode_rows(old_batch, {"s": d})
        # decoding with the retained old snapshot still works
        self.assertEqual(
            decode_rows(old_batch, {"s": snapshot_v1}),
            [("drop",), ("keep",)],
        )
        # merging the old batch still carries the retracted term's data
        result = Merger(self.store).merge([old_batch], job_id="j9")
        merged = list(self.store.iter_rows(result.version))
        self.assertEqual(merged, [("drop",), ("keep",)])

    def test_unreferenced_terms_reclaimed(self):
        d = LocalDictionary()
        d.add("used")
        d.add("stale")  # never referenced by any row
        schema = [("s", STR)]
        b = Batch.from_rows(schema, [("used",), ("used",)], {"s": d})
        result = Merger(self.store).merge([b], job_id="j10")
        payload = self.store.load_dict(result.version)
        self.assertEqual(payload["columns"]["s"]["terms"], ["used"])
        # remap table marks the stale code as reclaimed (-1)
        vdir = self.store.version_dir(result.version)
        with open(os.path.join(vdir, "remap_00000.json")) as f:
            remap = json.load(f)
        stale_code = 1
        self.assertEqual(remap["columns"]["s"][stale_code], -1)

    def test_error_locates_row(self):
        d = LocalDictionary()
        d.add("ok")
        schema = [("s", STR)]
        b = Batch(schema, 5)
        for r in range(5):
            b.values["s"][r] = 0
        b.values["s"][3] = 99  # invalid code at row 3
        b.dicts["s"] = d
        b.dict_versions["s"] = d.version
        with self.assertRaises(MergeError) as ctx:
            Merger(self.store).merge([b], job_id="j11")
        self.assertEqual(ctx.exception.batch_index, 0)
        self.assertEqual(ctx.exception.row, 3)
        self.assertEqual(ctx.exception.code, 99)
        self.assertIn("row 3", str(ctx.exception))

    def test_deterministic_dictionary_order(self):
        def build():
            d1 = LocalDictionary()
            for w in ["pear", "apple", "kiwi"]:
                d1.add(w)
            d2 = LocalDictionary()
            for w in ["kiwi", "banana", "apple"]:
                d2.add(w)
            schema = [("s", STR)]
            b1 = Batch.from_rows(schema, [("pear",), ("apple",)], {"s": d1})
            b2 = Batch.from_rows(schema, [("banana",), ("kiwi",)], {"s": d2})
            return [b1, b2]

        s1 = self.make_store("det1")
        s2 = self.make_store("det2")
        r1 = Merger(s1).merge(build(), job_id="j")
        r2 = Merger(s2).merge(build(), job_id="j")
        with open(os.path.join(s1.version_dir(r1.version), "dict.json"), "rb") as f:
            p1 = f.read()
        with open(os.path.join(s2.version_dir(r2.version), "dict.json"), "rb") as f:
            p2 = f.read()
        self.assertEqual(p1, p2)
        payload = s1.load_dict(r1.version)
        self.assertEqual(
            payload["columns"]["s"]["terms"],
            ["apple", "banana", "kiwi", "pear"],
        )


class TestRobustness(TestCaseBase):
    @staticmethod
    def _random_batches(seed, nrows=200, max_chunk=37):
        rng = random.Random(seed)
        vocab = [f"w{i}" for i in range(23)]
        rows = []
        for i in range(nrows):
            rows.append(
                (
                    rng.choice(vocab + [None]),
                    rng.randrange(-10**6, 10**6),
                    rng.choice([None, rng.random(), float("nan")]),
                )
            )
        schema = [("s", STR), ("n", INT), ("f", FLOAT)]
        batches = []
        i = 0
        k = 0
        while i < nrows:
            size = rng.randrange(1, max_chunk)
            chunk = rows[i : i + size]
            d = LocalDictionary()
            for w in rng.sample(vocab, len(vocab)):  # random local order
                d.add(w)
            batches.append(
                Batch.from_rows(schema, chunk, {"s": d})
            )
            i += size
            k += 1
        return batches, rows

    def test_random_chunking_matches_reference(self):
        for seed in (1, 2, 3):
            store = self.make_store(f"chunk{seed}")
            batches, _ = self._random_batches(seed)
            result = Merger(store).merge(batches, job_id="j")
            merged = list(store.iter_rows(result.version))
            self.assertTrue(rows_equal(merged, reference_rows(batches)))

    def test_spill_matches_reference(self):
        batches, _ = self._random_batches(7, nrows=300)
        config = MergeConfig(memory_budget_bytes=512)  # force spilling
        result = Merger(self.store, config).merge(batches, job_id="spill")
        merged = list(self.store.iter_rows(result.version))
        self.assertTrue(rows_equal(merged, reference_rows(batches)))
        self.assertGreater(result.accounting["spill_bytes"], 0)

    def test_cancel_and_resume_matches_reference(self):
        for cancel_phase in ("collect", "encode"):
            store = self.make_store(f"cancel-{cancel_phase}")
            batches, _ = self._random_batches(11, nrows=150)
            controller = CancelController()
            seen = {"n": 0}

            def cb(phase, index):
                if phase == cancel_phase:
                    seen["n"] += 1
                    if seen["n"] == 2:
                        controller.cancel()

            config = MergeConfig(
                memory_budget_bytes=1024,
                controller=controller,
                progress_callback=cb,
            )
            with self.assertRaises(MergeCancelled):
                Merger(store, config).merge(batches, job_id="job")
            # resume with a fresh merger, same job id
            result = Merger(store).merge(batches, job_id="job")
            merged = list(store.iter_rows(result.version))
            self.assertTrue(rows_equal(merged, reference_rows(batches)))

    def test_atomic_publish_crash_keeps_old_version(self):
        batches, _ = self._random_batches(5, nrows=60)
        # first publish succeeds -> version 1
        r1 = Merger(self.store).merge(batches[:2], job_id="first")
        self.assertEqual(self.store.current_version(), r1.version)

        # second publish crashes right before the atomic commit
        merger = Merger(self.store)

        def boom():
            raise RuntimeError("simulated crash")

        merger.fault_before_commit = boom
        with self.assertRaises(RuntimeError):
            merger.merge(batches, job_id="second")
        # old version still current and fully readable
        self.assertEqual(self.store.current_version(), r1.version)
        rows_v1 = list(self.store.iter_rows(r1.version))
        self.assertTrue(rows_equal(rows_v1, reference_rows(batches[:2])))

        # resume completes the publish atomically
        r2 = Merger(self.store).merge(batches, job_id="second")
        self.assertEqual(self.store.current_version(), r2.version)
        merged = list(self.store.iter_rows(r2.version))
        self.assertTrue(rows_equal(merged, reference_rows(batches)))
        # old version still decodable afterwards
        self.assertTrue(
            rows_equal(
                list(self.store.iter_rows(r1.version)),
                reference_rows(batches[:2]),
            )
        )


class TestAccounting(TestCaseBase):
    def test_byte_accounting(self):
        batches, _ = TestRobustness._random_batches(seed=3, nrows=120)
        config = MergeConfig(memory_budget_bytes=1024)
        result = Merger(self.store, config).merge(batches, job_id="acct")
        vdir = self.store.version_dir(result.version)
        acct = result.accounting

        dict_size = os.path.getsize(os.path.join(vdir, "dict.json"))
        self.assertEqual(acct["dict_bytes"], dict_size)
        remap_size = sum(
            os.path.getsize(os.path.join(vdir, n))
            for n in os.listdir(vdir)
            if n.startswith("remap_")
        )
        self.assertEqual(acct["remap_bytes"], remap_size)
        output_size = sum(
            os.path.getsize(os.path.join(vdir, n))
            for n in os.listdir(vdir)
            if n.startswith("batch_")
        )
        self.assertEqual(acct["output_bytes"], output_size)
        self.assertGreater(acct["spill_bytes"], 0)
        # persisted copy matches
        self.assertEqual(self.store.accounting(result.version), acct)


if __name__ == "__main__":
    unittest.main()
