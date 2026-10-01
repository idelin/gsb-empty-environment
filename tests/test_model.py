import unittest
from fractions import Fraction

from budget_coverage import Problem


def base_problem(**kw):
    args = dict(
        element_weights=[5, 4, 4, 3],
        set_ids=list("ABC"),
        set_costs=[1, 2, 3],
        set_elements=[{0, 1}, {1, 2}, {2, 3}],
        budget=6,
    )
    args.update(kw)
    return Problem.create(**args)


class TestModel(unittest.TestCase):
    def test_profit_counts_overlap_once(self):
        p = base_problem()
        # 元素 1 被 A、B 重复覆盖，只计一次
        self.assertEqual(p.profit([0, 1]), Fraction(13))

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            base_problem(set_costs=[1.5, 2, 3])
        with self.assertRaises(TypeError):
            base_problem(element_weights=[0.5, 4, 4, 3])

    def test_rational_string_accepted(self):
        p = base_problem(set_costs=["1/2", 2, 3])
        self.assertEqual(p.sets[0].cost, Fraction(1, 2))

    def test_violations_cover_all_constraint_kinds(self):
        p = base_problem(budget=1, dependencies={"A": ["B"]},
                         mutex_groups=[["A", "C"]], min_coverage={3: 2})
        text = "\n".join(p.violations([0, 2]))
        self.assertIn("预算", text)
        self.assertIn("依赖", text)
        self.assertIn("互斥", text)
        self.assertIn("最低要求", text)

    def test_empty_set_adds_no_profit(self):
        p = base_problem(set_elements=[set(), {1, 2}, {2, 3}])
        self.assertEqual(p.profit([0]), 0)
        self.assertEqual(p.profit([0, 1]), p.profit([1]))

    def test_rational_profit_is_exact(self):
        p = base_problem(element_weights=[Fraction(1, 3), Fraction(1, 6), 0, 0])
        self.assertEqual(p.profit([0]), Fraction(1, 2))

    def test_delete_set_remaps_and_bumps_version(self):
        p = base_problem(dependencies={"C": ["B"]})
        v0 = p.version
        p.delete_set("A")
        self.assertEqual(p.version, v0 + 1)
        self.assertEqual(p.ids, ("B", "C"))
        self.assertTrue(p.violations([1]))  # 选 C 不选 B 仍违反依赖
        p.delete_set("B")
        self.assertEqual(p.deps, {})  # 依赖对象被删除后依赖消失

    def test_unknown_ids_rejected(self):
        with self.assertRaises(ValueError):
            base_problem(dependencies={"A": ["ZZ"]})
        with self.assertRaises(ValueError):
            base_problem(mutex_groups=[["A", "ZZ"]])


if __name__ == "__main__":
    unittest.main()
