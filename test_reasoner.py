#!/usr/bin/env python3
"""reasoner 引擎的单元测试。所有数据均为合成数据。"""
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from reasoner import Engine, format_result, insert_minimal


def supports_of(engine):
    """计算并格式化为 {fact: [[sources...], ...]} 便于断言。"""
    supports = engine.compute_supports()
    return format_result(supports, engine.truncated,
                         engine.steps, engine.budget)


class AcceptanceCycleTests(unittest.TestCase):
    """验收边界：来源 s->A，规则 A->B、B->A。"""

    def build(self):
        engine = Engine()
        engine.assert_source("s", ["A"])
        engine.add_rule(["A"], "B")
        engine.add_rule(["B"], "A")
        return engine

    def test_initial_cycle_supported_by_s(self):
        result = supports_of(self.build())
        self.assertEqual(result["facts"]["A"], [["s"]])
        self.assertEqual(result["facts"]["B"], [["s"]])
        self.assertFalse(result["truncated"])

    def test_revoke_s_removes_a_and_b(self):
        engine = self.build()
        engine.revoke_source("s")
        result = supports_of(engine)
        self.assertEqual(result["facts"], {})

    def test_independent_source_keeps_cycle_alive(self):
        engine = self.build()
        engine.assert_source("t", ["B"])
        engine.revoke_source("s")
        result = supports_of(engine)
        self.assertEqual(result["facts"]["A"], [["t"]])
        self.assertEqual(result["facts"]["B"], [["t"]])

    def test_cycle_without_base_source_is_empty(self):
        engine = Engine()
        engine.add_rule(["A"], "B")
        engine.add_rule(["B"], "A")
        result = supports_of(engine)
        self.assertEqual(result["facts"], {})


class SupportSemanticsTests(unittest.TestCase):
    def test_multiple_independent_sources(self):
        engine = Engine()
        engine.assert_source("s", ["A"])
        engine.assert_source("t", ["A"])
        result = supports_of(engine)
        self.assertEqual(result["facts"]["A"], [["s"], ["t"]])

    def test_revoke_keeps_facts_with_remaining_support(self):
        engine = Engine()
        engine.assert_source("s", ["A"])
        engine.assert_source("t", ["A"])
        engine.add_rule(["A"], "B")
        engine.revoke_source("s")
        result = supports_of(engine)
        self.assertEqual(result["facts"]["A"], [["t"]])
        self.assertEqual(result["facts"]["B"], [["t"]])

    def test_inclusion_minimality(self):
        # s 单独即可推出 C；{s,t} 不是极小集合，应被剔除。
        engine = Engine()
        engine.assert_source("s", ["A", "B"])
        engine.assert_source("t", ["B"])
        engine.add_rule(["A", "B"], "C")
        result = supports_of(engine)
        self.assertEqual(result["facts"]["C"], [["s"]])

    def test_ordering_by_size_then_lexicographic(self):
        engine = Engine()
        engine.assert_source("b", ["X"])
        engine.assert_source("a", ["Y"])
        engine.assert_source("c", ["Y"])
        engine.assert_source("d", ["Z"])
        engine.add_rule(["X", "Y"], "Z")
        result = supports_of(engine)
        self.assertEqual(result["facts"]["Z"],
                         [["d"], ["a", "b"], ["b", "c"]])

    def test_empty_premise_rule(self):
        engine = Engine()
        engine.add_rule([], "AXIOM")
        result = supports_of(engine)
        self.assertEqual(result["facts"]["AXIOM"], [[]])

    def test_exponential_growth_of_minimal_sets(self):
        # 每个 Xi 有两个独立来源，合取规则产生 2^n 个极小来源集合。
        n = 4
        engine = Engine()
        premises = []
        for i in range(n):
            engine.assert_source(f"s{i}", [f"X{i}"])
            engine.assert_source(f"t{i}", [f"X{i}"])
            premises.append(f"X{i}")
        engine.add_rule(premises, "Y")
        result = supports_of(engine)
        self.assertEqual(len(result["facts"]["Y"]), 2 ** n)
        for src_set in result["facts"]["Y"]:
            self.assertEqual(len(src_set), n)

    def test_insert_minimal_helper(self):
        sets = []
        self.assertTrue(insert_minimal(sets, frozenset({"a", "b"})))
        self.assertTrue(insert_minimal(sets, frozenset({"a"})))  # 更小的替换掉大的
        self.assertEqual(sets, [frozenset({"a"})])
        self.assertFalse(insert_minimal(sets, frozenset({"a", "c"})))  # 非极小
        self.assertFalse(insert_minimal(sets, frozenset({"a"})))  # 重复


class BudgetTests(unittest.TestCase):
    def test_budget_interrupt_marks_truncated(self):
        engine = Engine(budget=2)
        engine.assert_source("s", ["A"])
        engine.add_rule(["A"], "B")
        engine.add_rule(["B"], "C")
        result = supports_of(engine)
        self.assertTrue(result["truncated"])

    def test_generous_budget_not_truncated(self):
        engine = Engine(budget=10_000)
        engine.assert_source("s", ["A"])
        engine.add_rule(["A"], "B")
        result = supports_of(engine)
        self.assertFalse(result["truncated"])
        self.assertEqual(result["facts"]["B"], [["s"]])

    def test_no_budget_never_truncates(self):
        engine = Engine()
        engine.assert_source("s", ["A"])
        result = supports_of(engine)
        self.assertFalse(result["truncated"])


class CliTests(unittest.TestCase):
    def run_cli(self, payload):
        with tempfile.NamedTemporaryFile(
                "w", suffix=".json", delete=False) as fh:
            json.dump(payload, fh)
            path = fh.name
        try:
            proc = subprocess.run(
                [sys.executable, "reasoner.py", path],
                capture_output=True, text=True, check=True)
            return json.loads(proc.stdout)
        finally:
            Path(path).unlink()

    def test_cli_acceptance_scenario(self):
        out = self.run_cli({
            "sources": {"s": ["A"], "t": ["B"]},
            "rules": [
                {"premises": ["A"], "conclusion": "B"},
                {"premises": ["B"], "conclusion": "A"},
            ],
            "operations": [{"op": "revoke", "source": "s"}],
        })
        self.assertEqual(out["facts"]["A"], [["t"]])
        self.assertEqual(out["facts"]["B"], [["t"]])
        self.assertFalse(out["truncated"])

    def test_cli_budget_flag(self):
        out = self.run_cli({
            "sources": {"s": ["A"]},
            "rules": [{"premises": ["A"], "conclusion": "B"}],
            "budget": 1,
        })
        self.assertTrue(out["truncated"])


if __name__ == "__main__":
    unittest.main()
