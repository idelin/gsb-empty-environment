import unittest

from ivm import (
    DuplicateRowError, Filter, GroupBy, Join, NegativeMultiplicityError,
    Project, Relation, UnknownRowError, View, ge, lt,
)


def chain(*nodes):
    for src, dst in zip(nodes, nodes[1:]):
        if isinstance(dst, Join):
            src.outputs.append(dst.feed_left)
        else:
            src.outputs.append(dst.feed)
    return nodes[-1]


class FilterProjectTest(unittest.TestCase):
    def test_filter_passes_matching_only(self):
        rel = Relation("r")
        view = chain(rel, Filter(ge(1, 18)), View("v"))
        rel.load([("a", ("x", 20)), ("b", ("y", 10)), ("c", ("z", None))])
        self.assertEqual(view.state, {("x", 20): 1})

    def test_projection_collapses_duplicates_into_multiplicity(self):
        rel = Relation("r")
        view = chain(rel, Project((0,)), View("v"))
        rel.load([("a", ("x", 1)), ("b", ("x", 2)), ("c", ("y", 3))])
        self.assertEqual(view.state, {("x",): 2, ("y",): 1})
        rel.delete("a")
        self.assertEqual(view.state, {("x",): 1, ("y",): 1})
        rel.delete("b")
        self.assertEqual(view.state, {("y",): 1})

    def test_null_comparison_is_false(self):
        rel = Relation("r")
        view = chain(rel, Filter(lt(1, 100)), View("v"))
        rel.load([("a", ("x", None)), ("b", ("y", 5))])
        self.assertEqual(view.state, {("y", 5): 1})


class JoinTest(unittest.TestCase):
    def build(self):
        left, right = Relation("l"), Relation("r")
        join = Join(0, 0)
        view = View("v")
        left.outputs.append(join.feed_left)
        right.outputs.append(join.feed_right)
        join.outputs.append(view.feed)
        return left, right, join, view

    def test_insert_multiplied_by_other_side_duplicates(self):
        left, right, join, view = self.build()
        # 右侧三行同键同值 -> 重数 3；左侧新增一行应产生 1*3 的输出
        right.load([("r1", (7, "a")), ("r2", (7, "a")), ("r3", (7, "a"))])
        left.insert("l1", (7, "L"))
        self.assertEqual(view.state, {(7, "L", 7, "a"): 3})
        # 左侧再插两行同值 -> 左侧重数 3，右侧重数 3，输出重数 9
        left.load([("l2", (7, "L")), ("l3", (7, "L"))])
        self.assertEqual(view.state, {(7, "L", 7, "a"): 9})
        self.assertEqual(join.state, {(7, "L", 7, "a"): 9})

    def test_delete_last_supporter_retracts_output(self):
        left, right, join, view = self.build()
        left.load([("l1", (1, "a")), ("l2", (1, "a"))])
        right.insert("r1", (1, "b"))
        self.assertEqual(view.state, {(1, "a", 1, "b"): 2})
        left.delete("l2")
        self.assertEqual(view.state, {(1, "a", 1, "b"): 1})
        left.delete("l1")
        self.assertEqual(view.state, {})
        self.assertEqual(view.log[-1], ((1, "a", 1, "b"), -1))

    def test_null_key_never_matches(self):
        left, right, join, view = self.build()
        left.load([("l1", (None, "a")), ("l2", (1, "b"))])
        right.load([("r1", (None, "c")), ("r2", (1, "d"))])
        self.assertEqual(view.state, {(1, "b", 1, "d"): 1})

    def test_right_side_delete_retracts(self):
        left, right, join, view = self.build()
        left.insert("l1", (1, "a"))
        right.load([("r1", (1, "x")), ("r2", (1, "x"))])
        self.assertEqual(view.state, {(1, "a", 1, "x"): 2})
        right.delete("r1")
        self.assertEqual(view.state, {(1, "a", 1, "x"): 1})


class GroupByTest(unittest.TestCase):
    def build(self):
        rel = Relation("r")
        grp = GroupBy((0,), 1)
        view = View("v")
        rel.outputs.append(grp.feed)
        grp.outputs.append(view.feed)
        return rel, grp, view

    def test_count_sum_and_update_deltas(self):
        rel, grp, view = self.build()
        rel.load([("a", ("g", 10)), ("b", ("g", 20)), ("c", ("h", 5))])
        self.assertEqual(view.state, {("g", 2, 30): 1, ("h", 1, 5): 1})
        # 组更新表现为 撤回旧行 + 插入新行
        self.assertIn((("g", 1, 10), -1), view.log)
        self.assertIn((("g", 2, 30), 1), view.log)

    def test_group_retracted_when_count_hits_zero(self):
        rel, grp, view = self.build()
        rel.load([("a", ("g", 10)), ("b", ("g", 20))])
        rel.delete("a")
        self.assertEqual(view.state, {("g", 1, 20): 1})
        rel.delete("b")
        self.assertEqual(view.state, {})
        self.assertEqual(grp.groups, {})

    def test_null_key_forms_own_group_and_null_value_ignored_in_sum(self):
        rel, grp, view = self.build()
        rel.load([("a", (None, 1)), ("b", (None, None)), ("c", ("g", None))])
        self.assertEqual(view.state, {(None, 2, 1): 1, ("g", 1, 0): 1})


class ErrorTest(unittest.TestCase):
    def test_delete_unknown_row_id(self):
        rel = Relation("r")
        with self.assertRaises(UnknownRowError):
            rel.delete("nope")

    def test_double_delete_raises(self):
        rel = Relation("r")
        rel.insert("a", (1,))
        rel.delete("a")
        with self.assertRaises(UnknownRowError):
            rel.delete("a")

    def test_duplicate_row_id_insert(self):
        rel = Relation("r")
        rel.insert("a", (1,))
        with self.assertRaises(DuplicateRowError):
            rel.insert("a", (2,))

    def test_negative_multiplicity_raises(self):
        view = View("v")
        with self.assertRaises(NegativeMultiplicityError):
            view.feed([(("x",), -1)])

    def test_negative_multiplicity_in_operator(self):
        proj = Project((0,))
        with self.assertRaises(NegativeMultiplicityError):
            proj.feed([(("x", 1), -1)])


class BatchOrderTest(unittest.TestCase):
    def build(self):
        dept = Relation("dept")
        emp = Relation("emp")
        join = Join(1, 0)
        grp = GroupBy((4,), 2)
        view = View("v")
        emp.outputs.append(join.feed_left)
        dept.outputs.append(join.feed_right)
        join.outputs.append(grp.feed)
        grp.outputs.append(view.feed)
        dept.load([("d1", (1, "Eng")), ("d2", (2, "Ops"))])
        emp.load([("e1", ("Ann", 1, 100)), ("e2", ("Bob", 1, 150))])
        return emp, view

    def test_batch_swap_yields_same_final_view(self):
        batch_a = [("insert", "e3", ("Cy", 2, 120)), ("insert", "e4", ("Dan", 1, 80))]
        batch_b = [("delete", "e1", None), ("insert", "e5", ("Eve", 2, 90))]

        def run(order):
            emp, view = self.build()
            for i in order:
                for op, rid, row in (batch_a, batch_b)[i]:
                    getattr(emp, op)(rid, row) if row else getattr(emp, op)(rid)
            return view.state

        self.assertEqual(run([0, 1]), run([1, 0]))
        self.assertEqual(run([0, 1]), {("Eng", 2, 230): 1, ("Ops", 2, 210): 1})


class PipelineTest(unittest.TestCase):
    def test_filter_project_join_group_end_to_end(self):
        dept = Relation("dept")
        emp = Relation("emp")
        adults = Filter(ge(3, 30))
        emp_proj = Project((0, 1, 2))
        join = Join(1, 0)
        by_dept = Project((4, 2))
        grp = GroupBy((0,), 1)
        view = View("v")
        emp.outputs.append(adults.feed)
        adults.outputs.append(emp_proj.feed)
        emp_proj.outputs.append(join.feed_left)
        dept.outputs.append(join.feed_right)
        join.outputs.append(by_dept.feed)
        by_dept.outputs.append(grp.feed)
        grp.outputs.append(view.feed)

        dept.load([("d1", (1, "Eng")), ("d2", (2, "Ops"))])
        emp.load([
            ("e1", ("Ann", 1, 100, 35)),
            ("e2", ("Bob", 1, 150, 41)),
            ("e3", ("Cy", 2, 120, 28)),
            ("e4", ("Dan", 2, None, 50)),
            ("e5", ("Eve", None, 90, 60)),
        ])
        self.assertEqual(view.state, {("Eng", 2, 250): 1, ("Ops", 1, 0): 1})
        emp.load([("e6", ("Eve", 1, 100, 33)), ("e7", ("Eve", 1, 100, 33))])
        self.assertEqual(view.state, {("Eng", 4, 450): 1, ("Ops", 1, 0): 1})
        emp.delete("e4")
        self.assertEqual(view.state, {("Eng", 4, 450): 1})
        emp.delete("e6")
        emp.delete("e7")
        self.assertEqual(view.state, {("Eng", 2, 250): 1})


if __name__ == "__main__":
    unittest.main()
