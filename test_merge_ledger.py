"""merge_ledger 的单元测试。所有数据均为合成数据。"""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import merge_ledger as ml

REPO = Path(__file__).resolve().parent


def make_base():
    return ml.base_state([{"id": "e1", "amount": 10, "tags": ["a"]}])


def branch(base, ops):
    return ml.apply_ops(base, ops)


def set_amount(op_id, entry, amount):
    return {"op_id": op_id, "op": "set_amount", "entry": entry, "amount": amount}


def add_tag(op_id, entry, tag):
    return {"op_id": op_id, "op": "add_tag", "entry": entry, "tag": tag}


def remove_tag(op_id, entry, tag, removes=None):
    op = {"op_id": op_id, "op": "remove_tag", "entry": entry, "tag": tag}
    if removes is not None:
        op["removes"] = removes
    return op


class AcceptanceTest(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_amount_change_and_tag_add_merge(self):
        # 基线金额 10；左改 11，右只加标签 x → 合并为 11 且含 x。
        base = make_base()
        left = branch(base, [set_amount("L1", "e1", 11)])
        right = branch(base, [add_tag("R1", "e1", "x")])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertTrue(result["ok"])
        (entry,) = result["entries"]
        self.assertEqual(entry["amount"], 11)
        self.assertEqual(entry["tags"], ["a", "x"])
        self.assertEqual(result["conflicts"], [])

    def test_divergent_amounts_conflict(self):
        # 左右分别改 11 和 12 → 必须冲突，不能任意选后到者。
        base = make_base()
        left = branch(base, [set_amount("L1", "e1", 11)])
        right = branch(base, [set_amount("R1", "e1", 12)])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertFalse(result["ok"])
        (entry,) = result["entries"]
        self.assertIsNone(entry["amount"])
        (conflict,) = result["conflicts"]
        self.assertEqual(conflict["entry"], "e1")
        self.assertEqual(conflict["field"], "amount")
        self.assertEqual(conflict["base"], 10)
        self.assertEqual(conflict["values"], {"L1": 11, "R1": 12})


class AmountMergeTest(unittest.TestCase):
    def test_both_sides_same_amount_no_conflict(self):
        base = make_base()
        left = branch(base, [set_amount("L1", "e1", 11)])
        right = branch(base, [set_amount("R1", "e1", 11)])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertTrue(result["ok"])
        self.assertEqual(result["entries"][0]["amount"], 11)

    def test_one_side_unchanged_takes_other_side(self):
        base = make_base()
        left = branch(base, [])
        right = branch(base, [set_amount("R1", "e1", 12)])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertTrue(result["ok"])
        self.assertEqual(result["entries"][0]["amount"], 12)

    def test_no_changes_keeps_base(self):
        base = make_base()
        result = ml.resolve(ml.merge(base, branch(base, []), branch(base, [])))
        self.assertTrue(result["ok"])
        self.assertEqual(result["entries"][0]["amount"], 10)

    def test_intra_branch_last_write_wins_no_conflict(self):
        # 同一分支内连续修改是单一有序历史，以最后值为准，不构成冲突。
        base = make_base()
        left = branch(base, [set_amount("L1", "e1", 11), set_amount("L2", "e1", 13)])
        right = branch(base, [])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertTrue(result["ok"])
        self.assertEqual(result["entries"][0]["amount"], 13)


class TagMergeTest(unittest.TestCase):
    def test_remove_observed_base_tag(self):
        base = make_base()
        left = branch(base, [remove_tag("L1", "e1", "a")])
        right = branch(base, [])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], [])

    def test_remove_only_removes_observed_adds(self):
        # 左删除基线标签 a；右并发地重新添加 a（新操作 ID）。
        # 左的删除未观察到右的添加，故 a 仍然生效。
        base = make_base()
        left = branch(base, [remove_tag("L1", "e1", "a")])
        right = branch(base, [add_tag("R1", "e1", "a")])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], ["a"])

    def test_remove_of_unobserved_tag_is_noop(self):
        # 左添加 x；右在未观察到该添加时删除 x → 删除为空操作，x 保留。
        base = make_base()
        left = branch(base, [add_tag("L1", "e1", "x")])
        right = branch(base, [remove_tag("R1", "e1", "x")])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], ["a", "x"])

    def test_add_then_remove_within_branch(self):
        base = make_base()
        left = branch(base, [add_tag("L1", "e1", "t"), remove_tag("L2", "e1", "t")])
        right = branch(base, [])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], ["a"])

    def test_both_add_same_tag(self):
        base = make_base()
        left = branch(base, [add_tag("L1", "e1", "x")])
        right = branch(base, [add_tag("R1", "e1", "x")])
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], ["a", "x"])

    def test_explicit_removes_list_honored(self):
        base = make_base()
        left = branch(base, [add_tag("L1", "e1", "x")])
        right = branch(
            base, [remove_tag("R1", "e1", "x", removes=["base:e1:a"])]
        )
        # 显式 removes 指向基线标签 a 的添加操作 → a 被移除，x 不受影响。
        result = ml.resolve(ml.merge(base, left, right))
        self.assertEqual(result["entries"][0]["tags"], ["x"])


class MergeAlgebraTest(unittest.TestCase):
    """幂等、交换、不丢冲突证据。"""

    def _conflicting_states(self):
        base = make_base()
        left = branch(base, [set_amount("L1", "e1", 11), add_tag("L2", "e1", "x")])
        right = branch(base, [set_amount("R1", "e1", 12), remove_tag("R2", "e1", "a")])
        return base, left, right

    def test_commutative(self):
        base, left, right = self._conflicting_states()
        self.assertEqual(
            ml.merge(base, left, right), ml.merge(base, right, left)
        )

    def test_idempotent(self):
        base, left, right = self._conflicting_states()
        merged = ml.merge(base, left, right)
        self.assertEqual(ml.merge(base, merged, merged), merged)

    def test_merge_with_base_is_identity(self):
        base, left, right = self._conflicting_states()
        merged = ml.merge(base, left, right)
        self.assertEqual(ml.merge(base, merged, ml.base_state([
            {"id": "e1", "amount": 10, "tags": ["a"]}
        ])), merged)

    def test_conflict_evidence_survives_remerge(self):
        # 冲突状态再次参与合并（与第三方分支）后，冲突证据不得丢失。
        base, left, right = self._conflicting_states()
        merged = ml.merge(base, left, right)
        third = branch(base, [add_tag("T1", "e1", "z")])
        remerged = ml.resolve(ml.merge(base, merged, third))
        self.assertFalse(remerged["ok"])
        (conflict,) = remerged["conflicts"]
        self.assertEqual(conflict["values"], {"L1": 11, "R1": 12})
        tags = remerged["entries"][0]["tags"]
        self.assertEqual(tags, ["x", "z"])  # a 被 R2 删除，x/z 保留

    def test_conflicting_op_ids_rejected(self):
        # 同一 op_id 在两个分支承载不同值：拒绝静默覆盖，显式报错。
        base = make_base()
        left = branch(base, [set_amount("X1", "e1", 11)])
        right = branch(base, [set_amount("X1", "e1", 12)])
        with self.assertRaises(ValueError):
            ml.merge(base, left, right)


class CliTest(unittest.TestCase):
    def run_cli(self, doc, *extra_args):
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(doc, fh)
            path = fh.name
        try:
            return subprocess.run(
                [sys.executable, str(REPO / "merge_ledger.py"), "merge", path,
                 *extra_args],
                capture_output=True,
                text=True,
            )
        finally:
            Path(path).unlink(missing_ok=True)

    def test_cli_clean_merge_exit_0(self):
        doc = {
            "base": [{"id": "e1", "amount": 10, "tags": []}],
            "left": [set_amount("L1", "e1", 11)],
            "right": [add_tag("R1", "e1", "x")],
        }
        proc = self.run_cli(doc)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertTrue(out["ok"])
        self.assertEqual(out["entries"][0]["amount"], 11)
        self.assertEqual(out["entries"][0]["tags"], ["x"])

    def test_cli_conflict_exit_1(self):
        doc = {
            "base": [{"id": "e1", "amount": 10, "tags": []}],
            "left": [set_amount("L1", "e1", 11)],
            "right": [set_amount("R1", "e1", 12)],
        }
        proc = self.run_cli(doc)
        self.assertEqual(proc.returncode, 1, proc.stderr)
        out = json.loads(proc.stdout)
        self.assertFalse(out["ok"])
        self.assertEqual(out["conflicts"][0]["values"], {"L1": 11, "R1": 12})

    def test_cli_emit_state_roundtrip_idempotent(self):
        doc = {
            "base": [{"id": "e1", "amount": 10, "tags": ["a"]}],
            "left": [set_amount("L1", "e1", 11)],
            "right": [add_tag("R1", "e1", "x")],
        }
        proc = self.run_cli(doc, "--emit-state")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        state = json.loads(proc.stdout)["state"]
        # 将输出的状态作为两个分支再次合并，结果应与原状态一致（幂等）。
        doc2 = {"base": doc["base"], "left": state, "right": state}
        proc2 = self.run_cli(doc2, "--emit-state")
        self.assertEqual(proc2.returncode, 0, proc2.stderr)
        self.assertEqual(json.loads(proc2.stdout)["state"], state)

    def test_cli_bad_input_exit_2(self):
        proc = self.run_cli({"base": [{"id": "e1", "amount": 10}],
                             "left": [{"op_id": "L1", "op": "bogus", "entry": "e1"}]})
        self.assertEqual(proc.returncode, 2)
        self.assertIn("error:", proc.stderr)


if __name__ == "__main__":
    unittest.main()
