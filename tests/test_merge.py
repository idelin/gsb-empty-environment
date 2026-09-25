import math
import os
import tempfile
import unittest
from pathlib import Path

from dictmerge import (
    CollisionSafeMap,
    Column,
    CancellationToken,
    DictBatch,
    EncodedChunk,
    EncodedColumn,
    InvalidDictionaryCodeError,
    LocalDictionary,
    MemoryBudget,
    MergeCancelled,
    MergeJob,
    TermSpec,
    WithdrawnTermError,
    decode_with_dictionary,
    split_batch_random,
)
from dictmerge.physical import PublishedDictionaryReader, RecodedReader


WORD = Column("word", "string")
NUMBER = Column("number", "int")
FLAG = Column("flag", "float")


def col(column, codes):
    return EncodedColumn(column, tuple(codes))


def dictionary_terms_ab():
    return (TermSpec("a-id", "a"), TermSpec("b-id", "b"))


def dictionary_terms_ba():
    return (TermSpec("b-id", "b"), TermSpec("a-id", "a"))


def make_two_batches():
    first = DictBatch(
        0,
        {
            WORD.name: LocalDictionary("v1", {WORD: dictionary_terms_ab()}),
        },
        (col(WORD, [0, 1, None, 0]),),
    )
    second = DictBatch(
        1,
        {
            WORD.name: LocalDictionary("v1", {WORD: dictionary_terms_ba()}),
        },
        (col(WORD, [0, 1, 0]),),
    )
    return first, second


def add_chunks(job, batches, seed=1, shuffle=True):
    chunks = []
    for batch in batches:
        chunks.extend(split_batch_random(batch, seed=seed + batch.batch_index, shuffle=shuffle))
    if shuffle:
        import random

        random.Random(seed).shuffle(chunks)
    for chunk in chunks:
        job.add_chunk(chunk)


class MergeDictionaryTests(unittest.TestCase):
    def test_code_zero_is_local_and_merge_equals_concatenation(self):
        with tempfile.TemporaryDirectory() as tempdir:
            first, second = make_two_batches()
            self.assertEqual(decode_with_dictionary(first)["word"][0], "a")
            self.assertEqual(decode_with_dictionary(second)["word"][0], "b")
            job = MergeJob(Path(tempdir) / "job", [first, second], memory_limit_bytes=128)
            add_chunks(job, [first, second], seed=3)
            result = job.finalize()
            self.assertEqual(
                result.decode()["word"],
                ("a", "b", None, "a", "b", "a", "b"),
            )
            with PublishedDictionaryReader(result.dictionary_path) as published:
                terms = [published.term("word", code).value for code in range(2)]
            self.assertEqual(terms, ["a", "b"])

    def test_null_empty_string_and_nan_are_distinct(self):
        batch = DictBatch(
            0,
            {
                WORD.name: LocalDictionary("v", {WORD: (TermSpec("empty", ""),)}),
                FLAG.name: LocalDictionary("v", {FLAG: (TermSpec("nan", float("nan")),)}),
            },
            (
                col(WORD, [None, 0, None, 0]),
                col(FLAG, [0, None, None, 0]),
            ),
        )
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [batch], memory_limit_bytes=128)
            add_chunks(job, [batch], seed=9)
            result = job.finalize()
            decoded = result.decode()
            self.assertEqual(decoded["word"], (None, "", None, ""))
            self.assertTrue(math.isnan(decoded["flag"][0]))
            self.assertTrue(math.isnan(decoded["flag"][3]))
            self.assertEqual(decoded["flag"][1:3], (None, None))
            bitmap = Path(result.recoded_batch_path).read_bytes()
            self.assertIn(0b0000_1001, bitmap)


    def test_multiple_columns_parse_and_decode(self):
        big = 2**63 + 17
        first = DictBatch(
            0,
            {
                WORD.name: LocalDictionary("v1", {
                    WORD: (TermSpec("a", "a"), TermSpec("b", "b"), TermSpec("empty", "")),
                }),
                NUMBER.name: LocalDictionary("v1", {
                    NUMBER: (TermSpec("big", big),),
                }),
            },
            (col(WORD, [0, 1, None, 2]), col(NUMBER, [0, 0, None, 0])),
        )
        second = DictBatch(
            1,
            {
                WORD.name: LocalDictionary("v1", {
                    WORD: (TermSpec("b", "b"), TermSpec("a", "a")),
                }),
                NUMBER.name: LocalDictionary("v1", {NUMBER: ()}),
            },
            (col(WORD, [0, 1, 0]), col(NUMBER, [None, None, None])),
        )
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [first, second], memory_limit_bytes=96)
            add_chunks(job, [first, second], seed=17)
            result = job.finalize()
            decoded = result.decode()
        self.assertEqual(decoded["word"], ("a", "b", None, "", "b", "a", "b"))
        self.assertEqual(decoded["number"], (big, big, None, big, None, None, None))

    def test_exact_large_integer_is_preserved(self):
        big_value = 2**127 + 19
        batch = DictBatch(
            0,
            {NUMBER.name: LocalDictionary("v", {NUMBER: (TermSpec("big", big_value),)})},
            (col(NUMBER, [0]),),
        )
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [batch], memory_limit_bytes=128)
            add_chunks(job, [batch], shuffle=False)
            self.assertEqual(job.finalize().decode()["number"], (big_value,))

    def test_invalid_code_error_identifies_rows(self):
        good = LocalDictionary("v1", {WORD: (TermSpec("a", "a"),)})
        batch = DictBatch(0, {WORD.name: good}, (col(WORD, [0, 7]),))
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [batch], memory_limit_bytes=128)
            job.add_chunk(EncodedChunk(0, 0, batch.columns))
            with self.assertRaises(InvalidDictionaryCodeError) as caught:
                job.finalize()
        self.assertEqual(caught.exception.batch_row, 2)
        self.assertEqual(caught.exception.global_row, 2)
        self.assertEqual(caught.exception.code, 7)

    def test_unreferenced_terms_are_reclaimed(self):
        terms = (TermSpec("used", "kept"), TermSpec("unused", "drop"))
        batch = DictBatch(
            0,
            {WORD.name: LocalDictionary("v", {WORD: terms})},
            (col(WORD, [0, None]),),
        )
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [batch], memory_limit_bytes=128)
            add_chunks(job, [batch], shuffle=False)
            result = job.finalize()
            with PublishedDictionaryReader(result.dictionary_path) as published:
                self.assertEqual(published.term_offsets["word"].__len__(), 1)
                self.assertEqual(published.term("word", 0).value, "kept")

    def test_same_literal_with_distinct_stable_identity_has_distinct_codes(self):
        left = DictBatch(
            0,
            {WORD.name: LocalDictionary("left", {WORD: (TermSpec("x-1", "x"),)})},
            (col(WORD, [0]),),
        )
        right = DictBatch(
            1,
            {WORD.name: LocalDictionary("right", {WORD: (TermSpec("x-2", "x"),)})},
            (col(WORD, [0]),),
        )
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [left, right], memory_limit_bytes=128)
            add_chunks(job, [left, right], seed=5)
            result = job.finalize()
            self.assertEqual(result.decode()["word"], ("x", "x"))
            with PublishedDictionaryReader(result.dictionary_path) as published:
                ids = [published.term("word", 0).term_id, published.term("word", 1).term_id]
            self.assertEqual(ids, ["x-1", "x-2"])

    def test_withdrawn_term_and_old_version(self):
        old_terms = (TermSpec("active", "yes"), TermSpec("removed", "gone"))
        old = LocalDictionary("old", {WORD: old_terms})
        old_batch = DictBatch(0, {WORD.name: old}, (col(WORD, [0, 1]),))
        self.assertEqual(decode_with_dictionary(old_batch)["word"], ("yes", "gone"))
        new = old.withdrawn_copy({"word": {"removed"}}, "new")
        with self.assertRaises(WithdrawnTermError):
            new.decode_code("word", 1)
        self.assertEqual(old.decode_code("word", 1, version="old"), "gone")


    def test_remap_table_pins_local_dictionary_version(self):
        first, second = make_two_batches()
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [first, second], memory_limit_bytes=128)
            add_chunks(job, [first, second], seed=6)
            result = job.finalize()
            from dictmerge.physical import RemapReader

            remap_dir = Path(result.dictionary_path).parent / "remaps"
            versions = {}
            for path in sorted(remap_dir.glob("*.rmap")):
                reader = RemapReader(path)
                try:
                    versions[reader.batch_index] = reader.version
                finally:
                    reader.close()
        self.assertEqual(versions, {0: "v1", 1: "v1"})
        self.assertEqual(first.dictionary("word").decode_code("word", 0), "a")
        self.assertEqual(second.dictionary("word").decode_code("word", 0), "b")


    def test_withdrawn_reference_during_merge_reports_row(self):
        terms = (TermSpec("active", "yes"), TermSpec("gone", "x"))
        dictionary = LocalDictionary("v", {WORD: (
            TermSpec("active", "yes"),
            TermSpec("gone", "x", withdrawn=True),
        )})
        batch = DictBatch(0, {WORD.name: dictionary}, (col(WORD, [0, 1]),))
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [batch], memory_limit_bytes=128)
            job.add_chunk(EncodedChunk(0, 0, batch.columns))
            with self.assertRaises(WithdrawnTermError) as caught:
                job.finalize()
        self.assertEqual(caught.exception.batch_row, 2)
        self.assertEqual(caught.exception.global_row, 2)
        self.assertEqual(caught.exception.term_id, "gone")

    def test_forced_hash_collision_map(self):
        mapping = CollisionSafeMap(hash_func=lambda key: 0)
        for index in range(20):
            mapping.put(f"key-{index}", index)
        self.assertEqual([mapping.get(f"key-{index}") for index in range(20)], list(range(20)))

        collided = LocalDictionary(
            "v",
            {WORD: tuple(TermSpec(f"id-{index}", f"value-{index}") for index in range(8))},
            hash_func=lambda key: 0,
        )
        for code in range(8):
            self.assertEqual(collided.code_for("word", f"id-{code}"), code)

    def test_spilling_and_byte_accounting(self):
        first, second = make_two_batches()
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [first, second], memory_limit_bytes=64)
            add_chunks(job, [first, second], seed=4)
            result = job.finalize()
            accounting = result.accounting_dict()
            self.assertGreater(accounting["spill_bytes_written"], 0)
            self.assertGreater(accounting["physical_dictionary_bytes"], 0)
            self.assertGreater(accounting["physical_remap_bytes"], 0)
            self.assertGreater(accounting["physical_recoded_bytes"], 0)
            self.assertLessEqual(accounting["peak_memory_bytes"], accounting["memory_limit_bytes"] * 4)

    def test_cancel_resume_random_chunks_match_reference(self):
        first, second = make_two_batches()
        reference = decode_with_dictionary(first)["word"] + decode_with_dictionary(second)["word"]

        class CancelAfter(CancellationToken):
            def __init__(self, limit):
                self.limit = limit
                self.calls = 0

            def is_cancelled(self):
                self.calls += 1
                return self.calls > self.limit

        with tempfile.TemporaryDirectory() as tempdir:
            workdir = Path(tempdir) / "job"
            job = MergeJob(workdir, [first, second], memory_limit_bytes=96)
            chunks = []
            chunks.extend(split_batch_random(first, seed=11, shuffle=True))
            chunks.extend(split_batch_random(second, seed=13, shuffle=True))
            for chunk in chunks:
                job.add_chunk(chunk)
            with self.assertRaises(MergeCancelled):
                job.finalize(CancelAfter(2))
            resumed = MergeJob.resume(workdir)
            with self.assertRaises(MergeCancelled):
                resumed.finalize(CancelAfter(2))
            resumed_again = MergeJob.resume(workdir)
            result = resumed_again.finalize()
            self.assertEqual(result.decode()["word"], reference)

    def test_memory_budget_accounts_peak(self):
        budget = MemoryBudget(10)
        budget.acquire(4)
        budget.acquire(3)
        self.assertEqual(budget.peak_bytes, 7)
        budget.release(3)
        self.assertEqual(budget.available, 6)

    def test_atomic_generation_contains_dictionary_and_remaps(self):
        first, second = make_two_batches()
        with tempfile.TemporaryDirectory() as tempdir:
            job = MergeJob(Path(tempdir) / "job", [first, second], memory_limit_bytes=128)
            add_chunks(job, [first, second], seed=2)
            result = job.finalize()
            generation = Path(result.dictionary_path).parent
            self.assertTrue((generation / "release.json").exists())
            remaps = list((generation / "remaps").glob("*.rmap"))
            self.assertEqual(len(remaps), 2)
            with RecodedReader(result.recoded_batch_path) as batch:
                self.assertEqual(batch.row_count, 7)


if __name__ == "__main__":
    unittest.main()
