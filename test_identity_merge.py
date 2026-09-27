#!/usr/bin/env python3.11
"""identity_merge 的单元测试。所有数据均为合成数据。"""

import io
import unittest

from identity_merge import IdentityMerger, run_commands


class TestAcceptanceBoundaries(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_chain_forms_one_group(self):
        # a-b 证据 e1、b-c 证据 e2 -> 三者同组，规范身份为最小 ID
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        self.assertEqual(m.canonical("a"), "a")
        self.assertEqual(m.canonical("b"), "a")
        self.assertEqual(m.canonical("c"), "a")

    def test_retract_splits_component(self):
        # 删 e2 后 c 独立，a 与 b 仍同组
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        m.retract("e2")
        self.assertEqual(m.canonical("a"), "a")
        self.assertEqual(m.canonical("b"), "a")
        self.assertEqual(m.canonical("c"), "c")  # c 独立，规范身份为自身

    def test_duplicate_edge_survives_single_retract(self):
        # 同一边有两条证据 e1/e3，撤回 e1 后边不应断
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("a", "b", "e3")
        m.add("b", "c", "e2")
        m.retract("e1")
        self.assertEqual(m.canonical("a"), "a")
        self.assertEqual(m.canonical("b"), "a")
        self.assertEqual(m.canonical("c"), "a")  # 边 a-b 仍在，全组连通
        # 再撤回 e3，边才真正断开
        m.retract("e3")
        self.assertEqual(m.canonical("b"), "b")
        self.assertEqual(m.canonical("c"), "b")
        self.assertEqual(m.canonical("a"), "a")


class TestCanonicalRecompute(unittest.TestCase):
    """验证撤回后受影响规范 ID 被重算（非增量并查集能做到的语义）。"""

    def test_canonical_shifts_after_split(self):
        m = IdentityMerger()
        m.add("a", "z", "e1")
        m.add("m", "z", "e2")
        self.assertEqual(m.canonical("z"), "a")
        m.retract("e1")  # a 离开分量，z 的规范身份应变小集合重算
        self.assertEqual(m.canonical("a"), "a")
        self.assertEqual(m.canonical("z"), "m")
        self.assertEqual(m.canonical("m"), "m")

    def test_retract_middle_edge_splits_into_two(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        m.add("c", "d", "e3")
        m.retract("e2")  # 从中间断开：{a,b} 与 {c,d}
        self.assertEqual(m.canonical("a"), "a")
        self.assertEqual(m.canonical("b"), "a")
        self.assertEqual(m.canonical("c"), "c")
        self.assertEqual(m.canonical("d"), "c")

    def test_unknown_node_is_its_own_canonical(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        self.assertEqual(m.canonical("ghost"), "ghost")


class TestEvidencePath(unittest.TestCase):
    def test_path_evidence_chain(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        path = m.evidence_path("c")
        self.assertEqual(path, [("c", "e2", "b"), ("b", "e1", "a")])

    def test_path_uses_surviving_evidence(self):
        # 重复边撤回一条后，路径应引用仍存在的证据
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("a", "b", "e3")
        m.retract("e1")
        path = m.evidence_path("b")
        self.assertEqual(path, [("b", "e3", "a")])

    def test_path_empty_for_canonical_and_unknown(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        self.assertEqual(m.evidence_path("a"), [])
        self.assertEqual(m.evidence_path("ghost"), [])

    def test_path_after_split(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        m.retract("e2")
        self.assertEqual(m.evidence_path("c"), [])  # c 已独立
        self.assertEqual(m.evidence_path("b"), [("b", "e1", "a")])


class TestBatchQuery(unittest.TestCase):
    def test_batch_matches_individual_queries(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        m.add("x", "y", "e3")
        results = m.batch_query(["c", "y", "solo"])
        self.assertEqual(
            results,
            [
                "QUERY c canonical=a path=c -[e2]-> b -[e1]-> a",
                "QUERY y canonical=x path=y -[e3]-> x",
                "QUERY solo canonical=solo path=solo",
            ],
        )

    def test_batch_reflects_retraction(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        m.add("b", "c", "e2")
        m.retract("e2")
        results = m.batch_query(["a", "b", "c"])
        self.assertEqual(results[0], "QUERY a canonical=a path=a")
        self.assertEqual(results[1], "QUERY b canonical=a path=b -[e1]-> a")
        self.assertEqual(results[2], "QUERY c canonical=c path=c")


class TestErrors(unittest.TestCase):
    def test_duplicate_evidence_id_rejected(self):
        m = IdentityMerger()
        m.add("a", "b", "e1")
        with self.assertRaises(ValueError):
            m.add("c", "d", "e1")

    def test_retract_unknown_evidence_rejected(self):
        m = IdentityMerger()
        with self.assertRaises(ValueError):
            m.retract("nope")

    def test_self_loop_rejected(self):
        m = IdentityMerger()
        with self.assertRaises(ValueError):
            m.add("a", "a", "e1")


class TestCli(unittest.TestCase):
    def test_end_to_end_script(self):
        script = "\n".join(
            [
                "# 合成示例数据",
                "ADD a b e1",
                "ADD b c e2",
                "QUERY c",
                "RETRACT e2",
                "QUERY c",
                "BATCH a b c",
                "",
            ]
        )
        out = io.StringIO()
        code = run_commands(io.StringIO(script), out)
        self.assertEqual(code, 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(
            lines,
            [
                "OK",
                "OK",
                "QUERY c canonical=a path=c -[e2]-> b -[e1]-> a",
                "OK",
                "QUERY c canonical=c path=c",
                "QUERY a canonical=a path=a",
                "QUERY b canonical=a path=b -[e1]-> a",
                "QUERY c canonical=c path=c",
            ],
        )

    def test_cli_error_returns_nonzero(self):
        out = io.StringIO()
        code = run_commands(io.StringIO("RETRACT ghost\n"), out)
        self.assertEqual(code, 1)


if __name__ == "__main__":
    unittest.main()
