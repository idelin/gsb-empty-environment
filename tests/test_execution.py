import unittest

from plan_dag import (
    AggItem,
    AggregateSpec,
    And,
    Arith,
    Cmp,
    ColumnRef,
    Coalesce,
    Executor,
    IsNull,
    Literal,
    Node,
    Not,
    Or,
    PlanDag,
    PlanExecutionError,
    ProjectItem,
    ProjectSpec,
    ScanSpec,
    FilterSpec,
    LimitSpec,
)
from plan_dag.builders import (
    build_salary_over_60_plan,
    build_salary_report_plan,
)
from plan_dag.sample_data import build_catalog


def execute(dag, root=None):
    catalog = build_catalog()
    bound = dag.bind(catalog)
    return Executor(catalog).execute(bound, root), bound


class BagSemanticsTests(unittest.TestCase):
    def test_scan_keeps_duplicate_rows(self):
        dag = PlanDag([Node("t", ScanSpec("employees"), ())], roots=["t"])
        rows, _ = execute(dag, "t")
        bob_rows = [r for r in rows if r[0] == 2]
        self.assertEqual(len(rows), 7)
        self.assertEqual(len(bob_rows), 2)  # duplicate Bob appears twice

    def test_join_multiplicity_product(self):
        # self-join employees on dept=10: 3 Bob-ish? build via left/right scans
        from plan_dag import JoinSpec, ProjectSpec, ProjectItem
        t1 = Node("l", ScanSpec("employees"), ())
        t2 = Node("r", ScanSpec("employees"), ())
        pl = Node("pl", ProjectSpec([ProjectItem("id", ColumnRef("id")),
                                    ProjectItem("dept", ColumnRef("dept"))]), ("l",))
        pr = Node("pr", ProjectSpec([ProjectItem("rid", ColumnRef("id")),
                                    ProjectItem("dept", ColumnRef("dept"))]), ("r",))
        j = Node("j", JoinSpec(["dept"], ["dept"], "inner"), ("pl", "pr"))
        dag = PlanDag([t1, t2, pl, pr, j], roots=["j"])
        rows, bound = execute(dag, "j")
        # dept 10: 3x3=9; dept 20: 2x2=4; dept 30: 1x1=1; NULL never matches
        self.assertEqual(len(rows), 14)
        self.assertEqual([f.name for f in bound.schema_of("j").fields],
                         ["id", "dept", "rid"])

    def test_left_outer_keeps_unmatched_with_nulls(self):
        rows, bound = execute(build_salary_report_plan(), "payroll_by_dept")
        by_dept = {r[0]: r for r in rows}
        self.assertIn(None, by_dept)  # Eli(NULL dept) and Fay(dept 30 unmatched)
        null_rows = by_dept[None]
        self.assertEqual(null_rows[1], 2)   # headcount of the single NULL group
        self.assertEqual(null_rows[2], 80)  # Fay salary NULL ignored by sum
        self.assertEqual(by_dept["Eng"][:3], ["Eng", 3, 220])


class NullSemanticsTests(unittest.TestCase):
    def test_comparison_null_yields_unknown(self):
        rows, _ = execute(build_salary_over_60_plan(), "s_proj")
        names = [r[0] for r in rows]
        self.assertEqual(names, ["Ann", "Cy", "Eli"])  # Fay NULL excluded, Bob 60 excluded

    def test_three_valued_and_or_not(self):
        t = Node("t", ScanSpec("employees"), ())
        # salary > 50 AND active: Di active=NULL but salary 60 -> unknown AND null
        f_and = Node("and",
                     FilterSpec(And(Cmp("gt", ColumnRef("salary"), Literal(90)),
                                    ColumnRef("active"))),
                     ("t",))
        rows, _ = execute(PlanDag([t, f_and], roots=["and"]), "and")
        # Ann salary 100 active true; Cy salary 200 active true;
        # Eli salary 80 fails first conjunct; Di salary 60 fails too.
        self.assertEqual([r[1] for r in rows], ["Ann", "Cy"])

        t2 = Node("t2", ScanSpec("employees"), ())
        f_or = Node("or",
                    FilterSpec(Or(IsNull(ColumnRef("active")),
                                  Cmp("gt", ColumnRef("salary"), Literal(150)))),
                    ("t2",))
        rows, _ = execute(PlanDag([t2, f_or], roots=["or"]), "or")
        self.assertEqual([r[1] for r in rows], ["Cy", "Di"])

        t3 = Node("t3", ScanSpec("employees"), ())
        f_not = Node("not",
                     FilterSpec(Not(Cmp("ge", ColumnRef("salary"), Literal(100)))),
                     ("t3",))
        rows, _ = execute(PlanDag([t3, f_not], roots=["not"]), "not")
        # NOT(salary >= 100): Ann(100) false, Cy(200) false;
        # Fay NULL -> unknown removed; Bob/Di/Eli true (Bob twice).
        self.assertEqual([r[1] for r in rows], ["Bob", "Di", "Eli", "Bob"])

    def test_coalesce(self):
        t = Node("t", ScanSpec("employees"), ())
        p = Node("p", ProjectSpec([
            ProjectItem("label", Coalesce(ColumnRef("salary"), Literal(0))),
        ]), ("t",))
        rows, _ = execute(PlanDag([t, p], roots=["p"]), "p")
        self.assertEqual([r[0] for r in rows], [100, 60, 200, 60, 80, 0, 60])

    def test_arithmetic_null_and_div_zero(self):
        t = Node("t", ScanSpec("employees"), ())
        p = Node("p", ProjectSpec([
            ProjectItem("d", Arith("div", Literal(1), ColumnRef("id"))),
        ]), ("t",))
        rows, bound = execute(PlanDag([t, p], roots=["p"]), "p")
        self.assertEqual(rows[0], [1.0])

        p2 = Node("p2", ProjectSpec([
            ProjectItem("boom", Arith("div", ColumnRef("id"), Literal(0))),
        ]), ("t",))
        dag = PlanDag([t, p2], roots=["p2"])
        catalog = build_catalog()
        with self.assertRaises(PlanExecutionError):
            Executor(catalog).execute(dag.bind(catalog), "p2")


class AggregateTests(unittest.TestCase):
    def test_count_ignores_null_count_star_counts_all(self):
        t = Node("t", ScanSpec("employees"), ())
        agg = Node("agg", AggregateSpec([
            AggItem("count_star", None, "n"),
            AggItem("count", ColumnRef("salary"), "paid"),
            AggItem("min", ColumnRef("salary"), "lo"),
            AggItem("max", ColumnRef("salary"), "hi"),
        ], group_keys=[]), ("t",))
        rows, bound = execute(PlanDag([t, agg], roots=["agg"]), "agg")
        self.assertEqual(rows[0], [7, 6, 60, 200])

    def test_group_preserves_first_seen_order_and_bags(self):
        rows, _ = execute(build_salary_report_plan(), "payroll_by_dept")
        self.assertEqual([r[0] for r in rows], ["Eng", "Sales", None])


class LimitTests(unittest.TestCase):
    def test_limit_and_shared_node_single_evaluation(self):
        dag = build_salary_report_plan()
        catalog = build_catalog()
        bound = dag.bind(catalog)
        ex = Executor(catalog)
        results = ex.execute_plan(bound)
        self.assertEqual(len(results["top2"]), 2)
        self.assertEqual(ex.last_context.scan_count, 2)


if __name__ == "__main__":
    unittest.main()
