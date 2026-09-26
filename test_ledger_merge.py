"""Unit tests for ledger_merge. All data is synthetic."""

import json
import os
import subprocess
import sys
import tempfile
import unittest

import ledger_merge as lm


def entry(entry_id, amount, tag_ops=None):
    return {"id": entry_id, "amount": amount, "tag_ops": tag_ops or []}


def add(op_id, tag):
    return {"op": "add", "op_id": op_id, "tag": tag}


def remove(op_id, removes):
    return {"op": "remove", "op_id": op_id, "removes": list(removes)}


def doc(*entries):
    return {"entries": list(entries)}


def merge(base, left, right):
    return lm.merge_documents(base, left, right)


class AcceptanceTests(unittest.TestCase):
    """The two acceptance boundaries from the specification."""

    def test_amount_change_plus_tag_add_merges(self):
        # Baseline amount 10; left changes to 11; right only adds tag x.
        base = doc(entry("e1", 10))
        left = doc(entry("e1", 11))
        right = doc(entry("e1", 10, [add("r1", "x")]))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "clean")
        self.assertEqual(result["conflicts"], [])
        merged = result["merged"][0]
        self.assertEqual(merged["amount"], 11)
        self.assertEqual(merged["tags"], ["x"])

    def test_divergent_amounts_conflict(self):
        # Left 11 vs right 12 must conflict; never silently pick a winner.
        base = doc(entry("e1", 10))
        left = doc(entry("e1", 11))
        right = doc(entry("e1", 12))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "conflict")
        merged = result["merged"][0]
        self.assertIsNone(merged["amount"])  # no arbitrary last-writer-wins
        self.assertEqual(len(result["conflicts"]), 1)
        conflict = result["conflicts"][0]
        self.assertEqual(conflict["entry_id"], "e1")
        self.assertEqual(conflict["field"], "amount")
        self.assertEqual(conflict["base"], 10)
        self.assertEqual(conflict["values"], [11, 12])  # both sides preserved


class AmountMergeTests(unittest.TestCase):
    def test_both_sides_same_change_is_clean(self):
        result = merge(doc(entry("e1", 10)), doc(entry("e1", 11)), doc(entry("e1", 11)))
        self.assertEqual(result["status"], "clean")
        self.assertEqual(result["merged"][0]["amount"], 11)

    def test_only_right_changes(self):
        result = merge(doc(entry("e1", 10)), doc(entry("e1", 10)), doc(entry("e1", 12)))
        self.assertEqual(result["status"], "clean")
        self.assertEqual(result["merged"][0]["amount"], 12)

    def test_no_change(self):
        result = merge(doc(entry("e1", 10)), doc(entry("e1", 10)), doc(entry("e1", 10)))
        self.assertEqual(result["status"], "clean")
        self.assertEqual(result["merged"][0]["amount"], 10)

    def test_multiple_entries_merge_independently(self):
        base = doc(entry("a", 1), entry("b", 2))
        left = doc(entry("a", 10), entry("b", 2))
        right = doc(entry("a", 1), entry("b", 20))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "clean")
        amounts = {e["id"]: e["amount"] for e in result["merged"]}
        self.assertEqual(amounts, {"a": 10, "b": 20})

    def test_one_entry_conflicts_other_still_merges(self):
        base = doc(entry("a", 1), entry("b", 2))
        left = doc(entry("a", 10), entry("b", 20))
        right = doc(entry("a", 99), entry("b", 2))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "conflict")
        amounts = {e["id"]: e["amount"] for e in result["merged"]}
        self.assertIsNone(amounts["a"])
        self.assertEqual(amounts["b"], 20)  # clean entry unaffected
        self.assertEqual([c["entry_id"] for c in result["conflicts"]], ["a"])


class TagSemanticsTests(unittest.TestCase):
    def test_concurrent_adds_union(self):
        base = doc(entry("e1", 10))
        left = doc(entry("e1", 10, [add("l1", "x")]))
        right = doc(entry("e1", 10, [add("r1", "y")]))
        result = merge(base, left, right)
        self.assertEqual(result["merged"][0]["tags"], ["x", "y"])

    def test_remove_only_cancels_observed_adds(self):
        # Base has tag x (op a1). Left removes a1. Right concurrently re-adds
        # x under a new op a2 that the remove never observed: x survives.
        base = doc(entry("e1", 10, [add("a1", "x")]))
        left = doc(entry("e1", 10, [add("a1", "x"), remove("r1", ["a1"])]))
        right = doc(entry("e1", 10, [add("a1", "x"), add("a2", "x")]))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "clean")
        self.assertEqual(result["merged"][0]["tags"], ["x"])

    def test_observed_remove_wins_when_no_concurrent_add(self):
        base = doc(entry("e1", 10, [add("a1", "x")]))
        left = doc(entry("e1", 10, [add("a1", "x"), remove("r1", ["a1"])]))
        right = doc(entry("e1", 10, [add("a1", "x")]))
        result = merge(base, left, right)
        self.assertEqual(result["merged"][0]["tags"], [])

    def test_remove_of_other_tag_unaffected(self):
        base = doc(entry("e1", 10, [add("a1", "x"), add("a2", "y")]))
        left = doc(entry("e1", 10, [add("a1", "x"), add("a2", "y"), remove("r1", ["a1"])]))
        right = doc(entry("e1", 10, [add("a1", "x"), add("a2", "y")]))
        result = merge(base, left, right)
        self.assertEqual(result["merged"][0]["tags"], ["y"])

    def test_same_op_id_different_payload_is_conflict(self):
        base = doc(entry("e1", 10))
        left = doc(entry("e1", 10, [add("o1", "x")]))
        right = doc(entry("e1", 10, [add("o1", "y")]))
        result = merge(base, left, right)
        self.assertEqual(result["status"], "conflict")
        self.assertEqual(result["conflicts"][0]["field"], "tag_ops")
        self.assertEqual(result["conflicts"][0]["op_id"], "o1")
        # Both payloads preserved as evidence.
        self.assertEqual(len(result["conflicts"][0]["values"]), 2)


class AlgebraicPropertyTests(unittest.TestCase):
    def _scenarios(self):
        base = doc(entry("e1", 10, [add("a1", "x")]), entry("e2", 5))
        left = doc(
            entry("e1", 11, [add("a1", "x"), remove("r1", ["a1"]), add("l1", "L")]),
            entry("e2", 5),
        )
        right = doc(
            entry("e1", 12, [add("a1", "x"), add("a2", "x")]),
            entry("e2", 7, [add("r9", "z")]),
        )
        return base, left, right

    def test_commutative(self):
        base, left, right = self._scenarios()
        self.assertEqual(merge(base, left, right), merge(base, right, left))

    def test_idempotent_deterministic(self):
        base, left, right = self._scenarios()
        first = merge(base, left, right)
        second = merge(base, left, right)
        self.assertEqual(first, second)
        self.assertEqual(
            json.dumps(first, sort_keys=True), json.dumps(second, sort_keys=True)
        )

    def test_merging_result_with_itself_is_fixpoint(self):
        # Re-merging the merged output (as both branches, against itself as
        # base) must not change anything: merge is idempotent as an operator.
        base, left, right = self._scenarios()
        result = merge(base, left, right)
        # Use only the cleanly merged entry (e2) as a fixpoint check; e1 has
        # a null amount from the conflict and is not a valid input snapshot.
        e2 = next(e for e in result["merged"] if e["id"] == "e2")
        snapshot = doc(entry(e2["id"], e2["amount"], e2["tag_ops"]))
        again = merge(snapshot, snapshot, snapshot)
        self.assertEqual(again["status"], "clean")
        self.assertEqual(again["merged"][0]["amount"], e2["amount"])
        self.assertEqual(again["merged"][0]["tags"], e2["tags"])
        self.assertEqual(again["merged"][0]["tag_ops"], e2["tag_ops"])

    def test_conflict_evidence_not_lost_on_remerge(self):
        # A conflict recorded in a first merge must reappear (not be silently
        # resolved) when the divergent inputs are merged again.
        base = doc(entry("e1", 10))
        left = doc(entry("e1", 11))
        right = doc(entry("e1", 12))
        first = merge(base, left, right)
        second = merge(base, left, right)
        self.assertEqual(first["conflicts"], second["conflicts"])
        conflict = first["conflicts"][0]
        self.assertEqual(conflict["base"], 10)
        self.assertEqual(conflict["values"], [11, 12])


class ValidationTests(unittest.TestCase):
    def test_mismatched_entry_ids_rejected(self):
        base = doc(entry("e1", 10))
        left = doc(entry("e2", 10))
        right = doc(entry("e1", 10))
        with self.assertRaises(lm.MergeInputError):
            merge(base, left, right)

    def test_non_numeric_amount_rejected(self):
        with self.assertRaises(lm.MergeInputError):
            merge(doc(entry("e1", "ten")), doc(entry("e1", 10)), doc(entry("e1", 10)))

    def test_bool_amount_rejected(self):
        with self.assertRaises(lm.MergeInputError):
            merge(doc(entry("e1", True)), doc(entry("e1", 10)), doc(entry("e1", 10)))

    def test_duplicate_op_id_within_snapshot_rejected(self):
        bad = doc(entry("e1", 10, [add("o1", "x"), add("o1", "y")]))
        with self.assertRaises(lm.MergeInputError):
            merge(bad, bad, bad)

    def test_malformed_op_rejected(self):
        bad = doc(entry("e1", 10, [{"op": "frobnicate", "op_id": "o1"}]))
        with self.assertRaises(lm.MergeInputError):
            merge(bad, bad, bad)


class CliTests(unittest.TestCase):
    def _write(self, directory, name, data):
        path = os.path.join(directory, name)
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(data, fh)
        return path

    def _run_cli(self, *args):
        return subprocess.run(
            [sys.executable, "ledger_merge.py", *args],
            capture_output=True,
            text=True,
            cwd=os.path.dirname(os.path.abspath(__file__)),
        )

    def test_cli_clean_merge_exit_0(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = self._write(tmp, "base.json", doc(entry("e1", 10)))
            left = self._write(tmp, "left.json", doc(entry("e1", 11)))
            right = self._write(tmp, "right.json", doc(entry("e1", 10, [add("r1", "x")])) )
            proc = self._run_cli(base, left, right)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            result = json.loads(proc.stdout)
            self.assertEqual(result["status"], "clean")
            self.assertEqual(result["merged"][0]["amount"], 11)
            self.assertEqual(result["merged"][0]["tags"], ["x"])

    def test_cli_conflict_exit_1(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = self._write(tmp, "base.json", doc(entry("e1", 10)))
            left = self._write(tmp, "left.json", doc(entry("e1", 11)))
            right = self._write(tmp, "right.json", doc(entry("e1", 12)))
            proc = self._run_cli(base, left, right)
            self.assertEqual(proc.returncode, 1, proc.stderr)
            result = json.loads(proc.stdout)
            self.assertEqual(result["status"], "conflict")
            self.assertEqual(result["conflicts"][0]["values"], [11, 12])

    def test_cli_bad_input_exit_2(self):
        with tempfile.TemporaryDirectory() as tmp:
            bad = self._write(tmp, "bad.json", {"no_entries": True})
            ok = self._write(tmp, "ok.json", doc(entry("e1", 10)))
            proc = self._run_cli(bad, ok, ok)
            self.assertEqual(proc.returncode, 2)
            self.assertIn("error:", proc.stderr)

    def test_cli_missing_file_exit_2(self):
        proc = self._run_cli("/nonexistent/a.json", "/nonexistent/b.json", "/nonexistent/c.json")
        self.assertEqual(proc.returncode, 2)

    def test_cli_output_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = self._write(tmp, "base.json", doc(entry("e1", 10)))
            left = self._write(tmp, "left.json", doc(entry("e1", 11)))
            right = self._write(tmp, "right.json", doc(entry("e1", 10)))
            out = os.path.join(tmp, "out.json")
            proc = self._run_cli(base, left, right, "-o", out)
            self.assertEqual(proc.returncode, 0, proc.stderr)
            with open(out, encoding="utf-8") as fh:
                result = json.load(fh)
            self.assertEqual(result["merged"][0]["amount"], 11)


if __name__ == "__main__":
    unittest.main()
