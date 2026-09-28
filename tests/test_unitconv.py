"""unittest suite for the transactional unit-conversion graph.

Run with:

    python3.11 -m unittest discover -s tests -v
"""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from fractions import Fraction
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from unitconv import (  # noqa: E402
    ContradictionError,
    DimensionConflictError,
    DimensionMismatchError,
    GraphState,
    NoConversionPathError,
    StatementError,
    UnknownUnitError,
    VersionedGraph,
    format_factor,
    parse_factor,
    parse_statement,
)
from unitconv.store import (  # noqa: E402
    STORE_FORMAT,
    append_batch,
    init_store,
    load_store,
    stored_batches,
)


class FactorAndStatementTests(unittest.TestCase):
    def test_integer_decimal_and_fraction_exact(self):
        self.assertEqual(parse_factor("2"), Fraction(2))
        self.assertEqual(parse_factor("0.5"), Fraction(1, 2))
        self.assertEqual(parse_factor("0.1"), Fraction(1, 10))
        self.assertEqual(parse_factor("1/6"), Fraction(1, 6))
        self.assertEqual(parse_factor("3/2"), Fraction(3, 2))

    def test_invalid_factors(self):
        for bad in ["", " ", "abc", "1/2/3", "-2", "0", "/6", "1/"]:
            with self.subTest(bad=bad):
                with self.assertRaises(StatementError):
                    parse_factor(bad)

    def test_factor_formatting(self):
        self.assertEqual(format_factor(Fraction(6)), "6")
        self.assertEqual(format_factor(Fraction(1, 6)), "1/6")
        self.assertEqual(format_factor(Fraction(10)), "10")

    def test_parse_statement_forms(self):
        self.assertEqual(parse_statement("dim: LENGTH m"),
                         ("dim", "LENGTH", "m"))
        self.assertEqual(parse_statement("A = 2 B"),
                         ("rule", "A", "B", Fraction(2)))
        self.assertEqual(parse_statement("A=B/6"),
                         ("rule", "A", "B", Fraction(1, 6)))
        self.assertEqual(parse_statement("A = 1/6 B"),
                         ("rule", "A", "B", Fraction(1, 6)))
        self.assertEqual(parse_statement("A = B"),
                         ("rule", "A", "B", Fraction(1)))
        self.assertEqual(parse_statement("x = .25 y"),
                         ("rule", "x", "y", Fraction(1, 4)))

    def test_parse_statement_errors(self):
        for bad in ["", "   ", "nope", "dim:", "dim: A",
                    "A = 2 3 B", "A = B * 2", "A = = 2 B"]:
            with self.subTest(bad=bad):
                with self.assertRaises(StatementError):
                    parse_statement(bad)


class AcceptanceBoundaryTests(unittest.TestCase):
    """The exact acceptance boundaries from the specification."""

    def setUp(self):
        self.graph = VersionedGraph()
        self.graph.batch(["A = 2 B", "B = 3 C"])

    def test_two_hops_imply_A_equals_6C(self):
        result = self.graph.convert("A", "C")
        self.assertEqual(result.factor, Fraction(6))
        self.assertEqual(self.graph.convert("A", "C", Fraction(1)).result,
                         Fraction(6))
        self.assertEqual(self.graph.convert("A", "C", Fraction(2)).result,
                         Fraction(12))

    def test_reverse_multi_hop(self):
        result = self.graph.convert("C", "A")
        self.assertEqual(result.factor, Fraction(1, 6))
        self.assertEqual(self.graph.convert("C", "A", Fraction(6)).result,
                         Fraction(1))
        self.assertEqual(self.graph.convert("C", "A", Fraction(1)).result,
                         Fraction(1, 6))

    def test_contradiction_rejected_and_graph_unchanged(self):
        before = self.graph.state()
        snapshot = (dict(before.parent), dict(before.ratio),
                    {u: list(e) for u, e in before.adj.items()},
                    before.version if hasattr(before, "version") else None)
        version_before = self.graph.version
        with self.assertRaises(ContradictionError) as ctx:
            self.graph.batch(["A = 5 C"])
        err = ctx.exception
        self.assertEqual(err.expected, Fraction(6))
        self.assertEqual(err.claimed, Fraction(5))
        self.assertEqual([u for u, _ in err.path], ["A", "B", "C"])
        self.assertEqual(err.path[0][1], Fraction(2))
        self.assertEqual(err.path[1][1], Fraction(3))
        self.assertIsNone(err.path[2][1])
        # Whole batch rejected: no new version, state identical.
        self.assertEqual(self.graph.version, version_before)
        after = self.graph.state()
        self.assertEqual(after.parent, snapshot[0])
        self.assertEqual(after.ratio, snapshot[1])
        self.assertEqual(
            {u: list(e) for u, e in after.adj.items()}, snapshot[2])
        # Original conversion still exactly 6.
        self.assertEqual(self.graph.convert("A", "C").factor, Fraction(6))

    def test_reciprocal_conversion_C_equals_A_over_6(self):
        # Both directions must be exact reciprocals.
        self.assertEqual(self.graph.convert("C", "A").factor,
                         Fraction(1) / self.graph.convert("A", "C").factor)

    def test_consistent_duplicate_rule_is_accepted(self):
        info = self.graph.batch(["A = 6 C"])
        self.assertEqual(info.version, 2)
        self.assertEqual(self.graph.convert("A", "C").factor, Fraction(6))

    def test_chain_order_does_not_matter(self):
        graph = VersionedGraph()
        graph.batch(["B = 3 C", "A = 2 B"])
        self.assertEqual(graph.convert("A", "C").factor, Fraction(6))

    def test_self_contradiction_rejected(self):
        graph = VersionedGraph()
        with self.assertRaises(ContradictionError):
            graph.batch(["A = 2 A"])

    def test_same_unit_query(self):
        result = self.graph.convert("B", "B", Fraction(7))
        self.assertEqual(result.factor, Fraction(1))
        self.assertEqual(result.result, Fraction(7))


class TransactionTests(unittest.TestCase):
    def test_syntax_error_aborts_entire_batch(self):
        graph = VersionedGraph()
        graph.batch(["A = 2 B"])
        with self.assertRaises(StatementError):
            graph.batch(["B = 9 C", "not a statement"])
        self.assertEqual(graph.version, 1)
        with self.assertRaises(UnknownUnitError):
            graph.convert("B", "C")

    def test_contradiction_aborts_entire_batch_and_keeps_earlier_rules(self):
        graph = VersionedGraph()
        graph.batch(["A = 2 B", "B = 3 C"])
        # First two statements of the new batch are fine; third closes a
        # contradictory cycle: none of the three may persist.
        with self.assertRaises(ContradictionError):
            graph.batch(["D = 4 E", "E = 2 F", "A = 5 C"])
        self.assertEqual(graph.version, 1)
        for ghost in ("D", "E", "F"):
            self.assertNotIn(ghost, graph.state().parent)
        self.assertEqual(graph.convert("A", "C").factor, Fraction(6))

    def test_new_units_from_rejected_batch_disappear(self):
        graph = VersionedGraph()
        graph.batch(["A = 2 B"])
        with self.assertRaises(ContradictionError):
            graph.batch(["A = 2 B", "X = 7 Y", "Y = 8 Z", "X = 1 Z"])
        for ghost in ("X", "Y", "Z"):
            self.assertNotIn(ghost, graph.state().parent)

    def test_two_independent_batches_both_commit(self):
        graph = VersionedGraph()
        graph.batch(["m = 100 cm"])
        graph.batch(["kg = 1000 g"])
        self.assertEqual(graph.convert("m", "cm").factor, Fraction(100))
        self.assertEqual(graph.convert("kg", "g").factor, Fraction(1000))


class DimensionTests(unittest.TestCase):
    def test_different_dimensions_refuse_query(self):
        graph = VersionedGraph()
        graph.batch(["dim: LENGTH m", "dim: LENGTH cm",
                     "m = 100 cm", "dim: MASS kg", "dim: MASS g",
                     "kg = 1000 g"])
        with self.assertRaises(DimensionMismatchError) as ctx:
            graph.convert("m", "kg")
        self.assertEqual(ctx.exception.dim_a, "LENGTH")
        self.assertEqual(ctx.exception.dim_b, "MASS")

    def test_rule_across_dimensions_rejected(self):
        graph = VersionedGraph()
        graph.batch(["dim: LENGTH m", "dim: MASS kg"])
        with self.assertRaises(DimensionMismatchError):
            graph.batch(["m = 2 kg"])

    def test_same_dimension_rule_allowed(self):
        graph = VersionedGraph()
        graph.batch(["dim: LENGTH m", "dim: LENGTH cm", "m = 100 cm"])
        self.assertEqual(graph.state().dimension_of("cm"), "LENGTH")
        self.assertEqual(graph.convert("m", "cm").factor, Fraction(100))

    def test_dimension_conflict_on_join_rejected_transactionally(self):
        graph = VersionedGraph()
        graph.batch(["dim: LENGTH m", "dim: MASS g"])
        with self.assertRaises((DimensionMismatchError, DimensionConflictError)):
            graph.batch(["dim: LENGTH m", "dim: MASS g", "m = 5 g"])
        self.assertEqual(graph.version, 1)

    def test_relabelling_one_unit_rejected(self):
        graph = VersionedGraph()
        with self.assertRaises(DimensionConflictError):
            graph.batch(["dim: LENGTH m", "dim: MASS m"])

    def test_unlabelled_vs_labelled_query_rejected_not_silently_guessed(self):
        graph = VersionedGraph()
        graph.batch(["dim: LENGTH m", "m = 100 cm", "x = 9 y"])
        with self.assertRaises(NoConversionPathError):
            graph.convert("m", "x")

    def test_dim_declaration_order_independent_within_batch(self):
        graph = VersionedGraph()
        graph.batch(["m = 100 cm", "dim: LENGTH m", "dim: LENGTH cm"])
        self.assertEqual(graph.state().dimension_of("cm"), "LENGTH")


class VersionTests(unittest.TestCase):
    def test_old_versions_remain_replayable(self):
        graph = VersionedGraph()
        graph.batch(["A = 2 B"])
        graph.batch(["B = 3 C"])
        graph.batch(["C = 4 D"])
        # Version 1 only knew A,B; version 2 reached A..C; version 3 adds D.
        self.assertEqual(graph.convert("A", "B", version=1).factor,
                         Fraction(2))
        self.assertEqual(graph.convert("A", "C", version=2).factor,
                         Fraction(6))
        self.assertEqual(graph.convert("A", "D", version=3).factor,
                         Fraction(24))
        with self.assertRaises(UnknownUnitError):
            graph.convert("A", "D", version=2)

    def test_version_zero_is_empty(self):
        graph = VersionedGraph()
        graph.batch(["A = 2 B"])
        self.assertEqual(graph.state(0).units(), [])

    def test_unknown_version_rejected(self):
        graph = VersionedGraph()
        with self.assertRaises(ValueError):
            graph.state(5)

    def test_commit_metadata(self):
        graph = VersionedGraph()
        info = graph.batch(["A = 2 B", "B = 3 C"], timestamp="2026-01-01T00:00:00+00:00")
        self.assertEqual(info.version, 1)
        self.assertEqual(info.statement_count, 2)
        self.assertEqual(graph.commits()[0].timestamp,
                         "2026-01-01T00:00:00+00:00")


class ExactnessTests(unittest.TestCase):
    def test_decimal_rules_are_exact_not_binary_floats(self):
        graph = VersionedGraph()
        graph.batch(["a = 0.1 b"])
        self.assertEqual(graph.convert("a", "b").factor, Fraction(1, 10))
        # Ten hops of 0.1 would drift under binary float arithmetic.
        stmts = [f"u{i} = 0.1 u{i + 1}" for i in range(10)]
        graph.batch(stmts)
        result = graph.convert("u0", "u10")
        self.assertEqual(result.factor, Fraction(1, 10 ** 10))

    def test_large_rational_chain_exact(self):
        graph = VersionedGraph()
        graph.batch(["p = 7 q", "q = 13 r", "r = 1/97 s"])
        self.assertEqual(graph.convert("p", "s").factor,
                         Fraction(7 * 13, 97))
        self.assertEqual(graph.convert("s", "p").factor,
                         Fraction(97, 7 * 13))

    def test_near_but_unequal_values_contradict_no_epsilon(self):
        graph = VersionedGraph()
        graph.batch(["A = 3 B"])
        # 3000000000000001/1000000000000000 is about 3.0 but not exactly 3.
        graph.batch(["B = 1000000000000000 C"])
        with self.assertRaises(ContradictionError):
            graph.batch(["A = 3000000000000001 C"])


class StoreTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = os.path.join(self.tmp.name, "store.json")

    def tearDown(self):
        self.tmp.cleanup()

    def test_init_load_empty(self):
        init_store(self.path)
        graph = load_store(self.path)
        self.assertEqual(graph.version, 0)
        with open(self.path, "r", encoding="utf-8") as handle:
            payload = json.load(handle)
        self.assertEqual(payload["format"], STORE_FORMAT)

    def test_double_init_fails(self):
        init_store(self.path)
        with self.assertRaises(FileExistsError):
            init_store(self.path)

    def test_append_and_replay(self):
        init_store(self.path)
        append_batch(self.path, ["A = 2 B", "B = 3 C"])
        graph = load_store(self.path)
        self.assertEqual(graph.convert("A", "C").factor, Fraction(6))
        batches = stored_batches(self.path)
        self.assertEqual(batches[0]["version"], 1)
        self.assertEqual(batches[0]["statements"],
                         ["A = 2 B", "B = 3 C"])

    def test_rejected_batch_not_written(self):
        init_store(self.path)
        append_batch(self.path, ["A = 2 B", "B = 3 C"])
        with open(self.path, encoding="utf-8") as handle:
            before = handle.read()
        with self.assertRaises(ContradictionError):
            append_batch(self.path, ["A = 5 C"])
        with open(self.path, encoding="utf-8") as handle:
            after = handle.read()
        self.assertEqual(before, after)

    def test_load_without_init_fails(self):
        with self.assertRaises(FileNotFoundError):
            load_store(self.path)


class CliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.store = os.path.join(self.tmp.name, "store.json")

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, *args, expect=0):
        env = dict(os.environ)
        env["PYTHONPATH"] = str(REPO_ROOT) + os.pathsep + env.get(
            "PYTHONPATH", "")
        proc = subprocess.run(
            [sys.executable, "-m", "unitconv", "-f", self.store, *args],
            capture_output=True, text=True, env=env, cwd=str(REPO_ROOT))
        if proc.returncode != expect:
            raise AssertionError(
                f"cmd {args!r} exit {proc.returncode} != {expect}\n"
                f"stdout:\n{proc.stdout}\nstderr:\n{proc.stderr}")
        return proc

    def test_end_to_end_acceptance(self):
        self._run("init")
        self._run("batch", "A = 2 B", "B = 3 C")
        proc = self._run("query", "A", "C")
        self.assertIn("1 A = 6 C", proc.stdout)
        proc = self._run("query", "C", "A", "6")
        self.assertIn("6 C = 1 A", proc.stdout)
        proc = self._run("batch", "A = 5 C", expect=3)
        self.assertIn("contradiction", proc.stderr.lower())
        # File unchanged: still two rules, still converts to 6.
        proc = self._run("query", "--json", "A", "C")
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["factor"], "6")
        self.assertEqual(len(payload["path"]), 3)

    def test_dimension_mismatch_exit_code(self):
        self._run("init")
        self._run("batch", "dim: LENGTH m", "dim: MASS kg")
        self._run("query", "m", "kg", expect=4)

    def test_unknown_unit_exit_code(self):
        self._run("init")
        self._run("query", "a", "b", expect=6)

    def test_batch_from_file_and_versions(self):
        self._run("init")
        rules_path = os.path.join(self.tmp.name, "rules.txt")
        with open(rules_path, "w", encoding="utf-8") as handle:
            handle.write("# synthetic sample data\nA = 2 B\nB = 3 C\n")
        self._run("batch", "--from-file", rules_path)
        proc = self._run("versions", "--json")
        versions = json.loads(proc.stdout)
        self.assertEqual([v["version"] for v in versions], [0, 1])
        proc = self._run("rules")
        self.assertIn("A = 2 B", proc.stdout)
        self.assertIn("B = 3 C", proc.stdout)

    def test_query_at_old_version(self):
        self._run("init")
        self._run("batch", "A = 2 B")
        self._run("batch", "B = 3 C")
        proc = self._run("query", "-v", "1", "A", "C", expect=6)
        self.assertIn("unknown unit", proc.stderr.lower())
        proc = self._run("query", "-v", "1", "A", "B")
        self.assertIn("1 A = 2 B", proc.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
