"""unittest suite for minirel: pruning evidence, NULL/3VL, multiset semantics."""
import unittest

from minirel import (
    Aggregate, And, Arith, Col, ColRef, Cmp, Filter, IsNull, Join, Lit, Not,
    Or, Project, Scan, Sort, Table, run,
)


def employees():
    return Table(
        "employees",
        ["emp_id", "dept_id", "mgr_id", "name", "salary", "nickname"],
        [
            (1, 10, None, "amy", 90000, "a"),
            (2, 10, 1, "bob", 55000, None),
            (3, 20, 1, "cy", None, "c"),
            (3, 20, 1, "cy", None, "c"),   # exact duplicate (multiset)
            (4, None, 2, "dan", 70000, "d"),
            (5, 20, None, "eli", 48000, None),
        ],
    )


def departments():
    return Table(
        "departments",
        ["dept_id", "dept_name", "budget", "location"],
        [
            (10, "eng", 1000000, "sf"),
            (20, "ops", None, "la"),
            (30, "hr", 250000, "ny"),
        ],
    )


def scan_evidence(ev, alias):
    return next(s for s in ev.scans if s.alias == alias)


class PruningTests(unittest.TestCase):
    def test_unused_columns_pruned_with_evidence(self):
        proj = Project(Scan(employees(), "e"),
                       [(Col(ColRef("e", "name")), "name")])
        schema, rows, ev = run(proj)
        se = scan_evidence(ev, "e")
        self.assertEqual([c for c, _ in se.kept], ["name"])
        self.assertEqual(
            sorted(se.pruned),
            ["dept_id", "emp_id", "mgr_id", "nickname", "salary"],
        )
        self.assertEqual(rows, [("amy",), ("bob",), ("cy",), ("cy",),
                                ("dan",), ("eli",)])

    def test_filter_is_implicit_dependency(self):
        filt = Filter(Scan(employees(), "e"),
                      Cmp(">", Col(ColRef("e", "salary")), Lit(50000)))
        proj = Project(filt, [(Col(ColRef("e", "name")), "name")])
        schema, rows, ev = run(proj)
        se = scan_evidence(ev, "e")
        kept = dict(se.kept)
        self.assertIn("salary", kept)  # needed by filter, absent from output
        self.assertNotIn("salary", schema)
        self.assertTrue(any("filter predicate" in r for r in kept["salary"]))
        self.assertEqual(rows, [("amy",), ("bob",), ("dan",)])

    def test_join_keys_kept_on_both_sides(self):
        e, d = Scan(employees(), "e"), Scan(departments(), "d")
        join = Join(e, d, Col(ColRef("e", "dept_id")),
                    Col(ColRef("d", "dept_id")))
        proj = Project(join, [(Col(ColRef("d", "dept_name")), "dept")])
        schema, rows, ev = run(proj)
        self.assertIn("dept_id", dict(scan_evidence(ev, "e").kept))
        self.assertIn("dept_id", dict(scan_evidence(ev, "d").kept))
        self.assertIn("location", scan_evidence(ev, "d").pruned)
        self.assertEqual(schema, ["dept"])

    def test_expression_columns_are_dependencies(self):
        # salary*12 used only inside a projection expression
        proj = Project(Scan(employees(), "e"),
                       [(Arith("*", Col(ColRef("e", "salary")), Lit(12)),
                         "annual")])
        schema, rows, ev = run(proj)
        self.assertIn("salary", dict(scan_evidence(ev, "e").kept))
        self.assertEqual(rows[0], (90000 * 12,))
        self.assertEqual(rows[2], (None,))  # NULL propagates through arithmetic


class SortTests(unittest.TestCase):
    def test_sort_column_retained_until_after_sort(self):
        sort = Sort(Scan(employees(), "e"),
                    [(Col(ColRef("e", "salary")), True)])  # DESC
        proj = Project(sort, [(Col(ColRef("e", "name")), "name")])
        schema, rows, ev = run(proj)
        self.assertEqual(schema, ["name"])  # sort key not in output
        self.assertEqual(rows, [("amy",), ("dan",), ("bob",), ("eli",),
                                ("cy",), ("cy",)])  # NULLs last on DESC
        self.assertTrue(any("retained through sort" in n for n in ev.notes))
        kept = dict(scan_evidence(ev, "e").kept)
        self.assertTrue(any("dropped after sort" in r
                            for r in kept["salary"]))

    def test_nulls_first_on_ascending_sort(self):
        sort = Sort(Scan(employees(), "e"),
                    [(Col(ColRef("e", "salary")), False)])  # ASC
        proj = Project(sort, [(Col(ColRef("e", "name")), "name")])
        _, rows, _ = run(proj)
        self.assertEqual(rows[:2], [("cy",), ("cy",)])  # NULLs first on ASC
        self.assertEqual(rows[-1], ("amy",))


class AggregateTests(unittest.TestCase):
    def test_count_star_with_zero_columns_is_not_empty(self):
        agg = Aggregate(Scan(employees(), "e"), aggs=[("count", None, "n")])
        schema, rows, ev = run(agg)
        se = scan_evidence(ev, "e")
        self.assertEqual(se.kept, [])  # no columns needed at all
        self.assertEqual(sorted(se.pruned),
                         ["dept_id", "emp_id", "mgr_id", "name",
                          "nickname", "salary"])
        self.assertEqual(rows, [(6,)])  # duplicates counted: bag semantics
        self.assertTrue(any("COUNT(*)" in n for n in ev.notes))

    def test_count_star_empty_input_still_one_row(self):
        filt = Filter(Scan(employees(), "e"),
                      Cmp(">", Col(ColRef("e", "salary")), Lit(10**9)))
        agg = Aggregate(filt, aggs=[("count", None, "n")])
        _, rows, _ = run(agg)
        self.assertEqual(rows, [(0,)])

    def test_count_col_ignores_nulls(self):
        agg = Aggregate(Scan(employees(), "e"), aggs=[
            ("count", None, "n_all"),
            ("count", Col(ColRef("e", "salary")), "n_sal"),
            ("sum", Col(ColRef("e", "salary")), "total"),
        ])
        _, rows, _ = run(agg)
        self.assertEqual(rows, [(6, 4, 90000 + 55000 + 70000 + 48000)])

    def test_null_group_keys_group_together(self):
        agg = Aggregate(
            Scan(employees(), "e"),
            group_by=[(Col(ColRef("e", "dept_id")), "dept")],
            aggs=[("count", None, "n")],
        )
        _, rows, _ = run(agg)
        self.assertEqual(rows, [(10, 2), (20, 3), (None, 1)])


class NullSemanticsTests(unittest.TestCase):
    def test_null_comparison_is_unknown_and_filtered_out(self):
        filt = Filter(Scan(employees(), "e"),
                      Cmp(">", Col(ColRef("e", "salary")), Lit(1)))
        proj = Project(filt, [(Col(ColRef("e", "name")), "name")])
        _, rows, _ = run(proj)
        self.assertEqual(rows, [("amy",), ("bob",), ("dan",), ("eli",)])

    def test_null_join_keys_never_match(self):
        e, d = Scan(employees(), "e"), Scan(departments(), "d")
        join = Join(e, d, Col(ColRef("e", "dept_id")),
                    Col(ColRef("d", "dept_id")))
        proj = Project(join, [(Col(ColRef("e", "name")), "name")])
        _, rows, _ = run(proj)
        names = [r[0] for r in rows]
        self.assertNotIn("dan", names)  # NULL dept_id joins nothing
        self.assertEqual(len(rows), 5)  # amy,bob,cy,cy,eli

    def test_three_valued_logic(self):
        filt = Filter(
            Scan(employees(), "e"),
            Or(Cmp("=", Col(ColRef("e", "name")), Lit("amy")),
               And(Cmp(">", Col(ColRef("e", "salary")), Lit(50000)),
                   Not(IsNull(Col(ColRef("e", "nickname")))))),
        )
        proj = Project(filt, [(Col(ColRef("e", "name")), "name")])
        _, rows, _ = run(proj)
        self.assertEqual(rows, [("amy",), ("dan",)])


class MultisetTests(unittest.TestCase):
    def test_duplicates_survive_scan_and_filter(self):
        filt = Filter(Scan(employees(), "e"),
                      Cmp("=", Col(ColRef("e", "name")), Lit("cy")))
        proj = Project(filt, [(Col(ColRef("e", "emp_id")), "i")])
        _, rows, _ = run(proj)
        self.assertEqual(rows, [(3,), (3,)])

    def test_join_multiplicity_multiplies(self):
        t1 = Table("t1", ["a"], [(1,), (1,)])
        t2 = Table("t2", ["a"], [(1,), (1,), (1,)])
        join = Join(Scan(t1, "x"), Scan(t2, "y"),
                    Col(ColRef("x", "a")), Col(ColRef("y", "a")))
        agg = Aggregate(join, aggs=[("count", None, "n")])
        _, rows, _ = run(agg)
        self.assertEqual(rows, [(6,)])  # 2 x 3


class SameNameTests(unittest.TestCase):
    def test_same_named_columns_distinguished_by_source(self):
        e, m = Scan(employees(), "e"), Scan(employees(), "m")
        join = Join(e, m, Col(ColRef("e", "mgr_id")),
                    Col(ColRef("m", "emp_id")))
        proj = Project(join, [
            (Col(ColRef("e", "name")), "employee"),
            (Col(ColRef("m", "name")), "manager"),
        ])
        schema, rows, ev = run(proj)
        self.assertEqual(rows, [("bob", "amy"), ("cy", "amy"),
                                ("cy", "amy"), ("dan", "bob")])
        self.assertIn("name", dict(scan_evidence(ev, "e").kept))
        self.assertIn("name", dict(scan_evidence(ev, "m").kept))

    def test_unqualified_duplicate_aliases_rejected(self):
        with self.assertRaises(ValueError):
            Join(Scan(employees()), Scan(employees()),
                 Col(ColRef("employees", "mgr_id")),
                 Col(ColRef("employees", "emp_id")))


if __name__ == "__main__":
    unittest.main()
