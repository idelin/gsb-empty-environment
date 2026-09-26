#!/usr/bin/env python3
"""identity_merge 的单元测试。所有数据均为合成数据。"""

import io
import unittest

from identity_merge import IdentityMerger, format_path, run_commands


class TestAcceptanceBoundaries(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_chain_forms_single_group(self):
        # a-b 证据 e1、b-c 证据 e2 -> a,b,c 同组，规范 ID 为最小值 a
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("b", "c", "e2")
        canonical = m.canonical_ids()
        self.assertEqual(canonical, {"a": "a", "b": "a", "c": "a"})

    def test_retract_splits_component(self):
        # 删除 e2 后 c 独立，a 与 b 仍同组
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("b", "c", "e2")
        self.assertTrue(m.retract("e2"))
        canonical = m.canonical_ids()
        self.assertEqual(canonical["a"], "a")
        self.assertEqual(canonical["b"], "a")
        self.assertEqual(canonical["c"], "c")
        self.assertIsNone(m.find_path("a", "c"))

    def test_duplicate_edge_survives_single_retract(self):
        # 同一边有两条证据时，撤回其中一条不应断边
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("a", "b", "e3")
        self.assertTrue(m.retract("e1"))
        canonical = m.canonical_ids()
        self.assertEqual(canonical["a"], "a")
        self.assertEqual(canonical["b"], "a")
        path = m.find_path("a", "b")
        self.assertIsNotNone(path)
        self.assertEqual(path, [("a", ""), ("b", "e3")])


class TestEvidenceSemantics(unittest.TestCase):
    def test_retract_unknown_evidence(self):
        m = IdentityMerger()
        self.assertFalse(m.retract("nope"))

    def test_retract_is_one_shot(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        self.assertTrue(m.retract("e1"))
        self.assertFalse(m.retract("e1"))
        # 边消失后主体仍存在，各自成为独立身份
        self.assertEqual(m.canonical_ids(), {"a": "a", "b": "b"})

    def test_evidence_cannot_move_edge(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        with self.assertRaises(ValueError):
            m.add_evidence("b", "c", "e1")

    def test_readd_same_evidence_same_edge_is_idempotent(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("b", "a", "e1")  # 无向边，视为同一证据重复添加
        self.assertTrue(m.retract("e1"))
        self.assertEqual(m.canonical_ids(), {"a": "a", "b": "b"})

    def test_self_loop_rejected(self):
        m = IdentityMerger()
        with self.assertRaises(ValueError):
            m.add_evidence("a", "a", "e1")

    def test_canonical_is_min_id(self):
        m = IdentityMerger()
        m.add_evidence("z", "m", "e1")
        m.add_evidence("m", "q", "e2")
        self.assertEqual(m.canonical("z"), "m")
        self.assertEqual(m.canonical("q"), "m")
        self.assertIsNone(m.canonical("absent"))

    def test_retract_middle_edge_splits_into_two(self):
        # a-b-c-d 链，撤回中间证据后分裂为 {a,b} 与 {c,d}
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("b", "c", "e2")
        m.add_evidence("c", "d", "e3")
        m.retract("e2")
        canonical = m.canonical_ids()
        self.assertEqual(canonical, {"a": "a", "b": "a", "c": "c", "d": "c"})


class TestPathEvidence(unittest.TestCase):
    def test_path_format(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        m.add_evidence("b", "c", "e2")
        path = m.find_path("a", "c")
        self.assertEqual(format_path(path), "a -[e1]- b -[e2]- c")

    def test_path_picks_deterministic_evidence(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e9")
        m.add_evidence("a", "b", "e3")
        path = m.find_path("a", "b")
        self.assertEqual(path, [("a", ""), ("b", "e3")])

    def test_path_unknown_node(self):
        m = IdentityMerger()
        m.add_evidence("a", "b", "e1")
        self.assertIsNone(m.find_path("a", "ghost"))


class TestBatchCLI(unittest.TestCase):
    def test_batch_scenario(self):
        script = "\n".join([
            "# 合成示例数据",
            "add a b e1",
            "add b c e2",
            "query a c",
            "canon c",
            "retract e2",
            "canon c",
            "query a b",
            "query a c",
            "retract e2",
            "add a b e3",
            "retract e1",
            "query a b",
            "canon ghost",
            "bogus line here",
        ])
        out = io.StringIO()
        run_commands(script.splitlines(), out)
        self.assertEqual(out.getvalue().splitlines(), [
            "OK add a b e1",
            "OK add b c e2",
            "SAME canonical=a path=a -[e1]- b -[e2]- c",
            "a",
            "OK retract e2",
            "c",
            "SAME canonical=a path=a -[e1]- b",
            "DIFFERENT canonical_a=a canonical_b=c",
            "ERROR retract e2 : unknown evidence",
            "OK add a b e3",
            "OK retract e1",
            "SAME canonical=a path=a -[e3]- b",
            "UNKNOWN ghost",
            "ERROR line 15: unrecognized command: bogus line here",
        ])


if __name__ == "__main__":
    unittest.main()
