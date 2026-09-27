"""symmc 的 unittest 测试套件。

运行：python3.11 -m unittest discover -s tests -v
"""

import unittest

from symmc import Checker, Model, SymmetryError, Verdict
from symmc.examples import (
    build_leader_model,
    build_mutex_model,
    leader_role,
    mutex_worker_role,
)


class CanonicalizationTest(unittest.TestCase):
    """置换规范化：同构进程交换状态只保留一个代表，ID 映射可还原。"""

    def test_swapped_isomorphic_states_share_one_representative(self):
        model = build_mutex_model(3)
        state = (0, (0, 2, 1))
        swapped = (0, (0, 1, 2))  # 交换进程 1 与 2 的局部状态
        canon_a, _ = model.canonicalize(state)
        canon_b, _ = model.canonicalize(swapped)
        self.assertEqual(canon_a, canon_b)
        self.assertEqual(canon_a, (0, (0, 1, 2)))

    def test_id_mapping_restores_original_state(self):
        model = build_mutex_model(3)
        for state in [(0, (2, 0, 1)), (1, (1, 2, 0)), (0, (0, 0, 0))]:
            canonical, mapping = model.canonicalize(state)
            self.assertEqual(model.restore(canonical, mapping), state)

    def test_canonicalize_is_idempotent(self):
        model = build_mutex_model(3)
        state = (1, (2, 0, 2))
        canonical, _ = model.canonicalize(state)
        again, _ = model.canonicalize(canonical)
        self.assertEqual(canonical, again)

    def test_symmetry_reduces_state_count(self):
        model = build_mutex_model(3)
        compressed = Checker(model, use_symmetry=True).run()
        plain = Checker(model, use_symmetry=False).run()
        self.assertLess(compressed.explored, plain.explored)


class LeaderRoleTest(unittest.TestCase):
    """特殊角色（领导者）不可随意置换，不与普通进程混同。"""

    def test_leader_position_not_permuted(self):
        model = build_leader_model(2)
        state = (0, (2, 1, 0))  # leader=2, w0=1, w1=0
        canonical, mapping = model.canonicalize(state)
        self.assertEqual(canonical[1][0], 2)  # 领导者位置保持不变
        self.assertEqual(mapping[0], 0)
        self.assertEqual(canonical, (0, (2, 0, 1)))  # 仅 worker 位被排序

    def test_swapping_leader_with_worker_changes_representative(self):
        model = build_leader_model(2)
        state = (0, (2, 1, 0))
        swapped = model.swap(state, 0, 2)  # leader 与 w1 交换
        self.assertNotEqual(model.canonicalize(state)[0],
                            model.canonicalize(swapped)[0])

    def test_mixed_role_group_rejected(self):
        roles = [leader_role(), mutex_worker_role(), mutex_worker_role()]
        with self.assertRaises(SymmetryError):
            Model("bad", roles, 0, lambda s: True, groups=[(0, 1, 2)])

    def test_invalid_groups_rejected(self):
        roles = [mutex_worker_role() for _ in range(3)]
        with self.assertRaises(SymmetryError):  # 越界
            Model("bad", roles, 0, lambda s: True, groups=[(1, 7)])
        with self.assertRaises(SymmetryError):  # 组间重叠
            Model("bad", roles, 0, lambda s: True, groups=[(0, 1), (1, 2)])
        with self.assertRaises(SymmetryError):  # 组过小
            Model("bad", roles, 0, lambda s: True, groups=[(1,)])


class SymmetryAuditTest(unittest.TestCase):
    """对称条件动态验证。"""

    def test_audit_passes_on_declared_groups(self):
        self.assertIs(build_mutex_model(3).audit_symmetry(), True)
        self.assertIs(build_leader_model(2).audit_symmetry(), True)

    def test_audit_undecided_when_state_cap_exceeded(self):
        model = build_mutex_model(4)
        self.assertIsNone(model.audit_symmetry(max_states=2))


class ConsistencyTest(unittest.TestCase):
    """压缩前后小模型性质一致。"""

    def check_consistent(self, model):
        compressed = Checker(model, use_symmetry=True).run()
        plain = Checker(model, use_symmetry=False).run()
        self.assertEqual(compressed.verdict, plain.verdict)
        return compressed, plain

    def test_safe_models(self):
        for model in (build_mutex_model(2), build_mutex_model(3),
                      build_leader_model(2)):
            compressed, plain = self.check_consistent(model)
            self.assertEqual(compressed.verdict, Verdict.SAFE)
            self.assertLess(compressed.explored, plain.explored)

    def test_buggy_models(self):
        for model in (build_mutex_model(2, buggy=True),
                      build_mutex_model(3, buggy=True),
                      build_leader_model(2, buggy=True)):
            compressed, _ = self.check_consistent(model)
            self.assertEqual(compressed.verdict, Verdict.VIOLATED)


class CounterexampleTest(unittest.TestCase):
    """反例：保留 ID 映射可还原，可在原始模型上重放。"""

    def setUp(self):
        self.model = build_mutex_model(3, buggy=True)
        self.result = Checker(self.model, use_symmetry=True).run()
        self.assertEqual(self.result.verdict, Verdict.VIOLATED)

    def test_counterexample_replays_on_original_model(self):
        self.assertTrue(self.result.replay(self.model))

    def test_counterexample_steps_preserve_id_mapping(self):
        for step in self.result.counterexample:
            self.assertEqual(
                Model.restore(step.canonical, step.mapping), step.state)

    def test_counterexample_is_concrete_valid_path(self):
        path = self.result.counterexample
        self.assertEqual(path[0].state, self.model.initial_state())
        for cur, nxt in zip(path, path[1:]):
            self.assertIn((nxt.pid, nxt.state),
                          list(self.model.successors(cur.state)))
        self.assertFalse(self.model.invariant(path[-1].state))

    def test_tampered_counterexample_fails_replay(self):
        import copy

        path = copy.deepcopy(self.result.counterexample)
        shared, locals_ = path[-1].state
        path[-1].state = (99, locals_)  # 篡改共享变量，映射/迁移均对不上
        tampered = type(self.result)(self.result.verdict, self.result.explored, path)
        self.assertFalse(tampered.replay(self.model))


class PendingTest(unittest.TestCase):
    """未搜索完就保持未决。"""

    def test_state_cap_yields_pending(self):
        model = build_mutex_model(4)
        result = Checker(model, max_states=3).run()
        self.assertEqual(result.verdict, Verdict.PENDING)

    def test_depth_cap_yields_pending(self):
        model = build_mutex_model(3)
        result = Checker(model, max_depth=1).run()
        self.assertEqual(result.verdict, Verdict.PENDING)

    def test_violation_before_cap_still_reported(self):
        model = build_mutex_model(3, buggy=True)
        result = Checker(model, max_states=10_000).run()
        self.assertEqual(result.verdict, Verdict.VIOLATED)

    def test_full_search_is_safe_not_pending(self):
        model = build_mutex_model(3)
        result = Checker(model, max_states=10_000).run()
        self.assertEqual(result.verdict, Verdict.SAFE)


if __name__ == "__main__":
    unittest.main()
