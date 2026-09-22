"""Behavioral tests for EXISTS / scalar aggregate decorrelation.

Covers multiset semantics, NULL handling, scope shadowing, three-valued
logic, aggregation, and rejection of syntax outside the supported subset.
"""
from __future__ import annotations

import unittest

from minisql import (
    Database,
    MiniSQLError,
    SQLSemanticError,
    SQLSyntaxError,
    Table,
    UnsupportedSQLError,
    execute,
)


def db() -> Database:
    dept = Table("dept", ("deptno", "dname"), [
        (10, "ACCOUNTING"),
        (20, "RESEARCH"),
        (30, "SALES"),
        (40, "OPERATIONS"),
    ])
    emp = Table("emp", ("empno", "ename", "deptno", "sal", "comm"), [
        (1, "CLARK", 10, 2450, None),
        (2, "KING", 10, 5000, None),
        (3, "SMITH", 20, 800, None),
        (4, "JONES", 20, 2975, None),
        (5, "FORD", 20, 3000, None),
        (6, "ALLEN", 30, 1600, 300),
        (7, "MARTIN", 30, 1250, 500),
        (8, "JAMES", 30, 950, None),
        (9, "ADAMS", None, 1100, None),     # NULL department
    ])
    return Database([dept, emp])


class ExistsTests(unittest.TestCase):
    def test_exists_does_not_duplicate_outer_rows(self) -> None:
        # Departments 10/20/30 have multiple employees; each must appear once.
        result = execute(db(), """
            select d.deptno from dept d
            where exists (select 1 from emp e where e.deptno = d.deptno)
        """)
        self.assertEqual(result.rows, [(10,), (20,), (30,)])

    def test_exists_preserves_outer_multiset(self) -> None:
        # Duplicated outer rows are independently preserved (multiset, not set).
        dup = Table("dept", ("deptno",), [(10,), (10,), (40,), (20,)])
        catalog = Database([dup, db().get("emp").__class__(
            "emp", db().get("emp").columns, list(db().get("emp").rows))])
        result = execute(catalog, """
            select d.deptno from dept d
            where exists (select 1 from emp e where e.deptno = d.deptno)
        """)
        self.assertEqual(result.rows, [(10,), (10,), (20,)])

    def test_not_exists_finds_no_match_group(self) -> None:
        result = execute(db(), """
            select d.deptno from dept d
            where not exists (select * from emp e
                              where e.deptno = d.deptno)
        """)
        self.assertEqual(result.rows, [(40,)])

    def test_null_correlation_value_never_matches(self) -> None:
        # SQL NULL = NULL is not TRUE, so ADAMS's NULL department matches none.
        result = execute(db(), """
            select e.ename from emp e
            where exists (select 1 from dept d
                          where d.deptno = e.deptno)
        """)
        names = [row[0] for row in result.rows]
        self.assertNotIn("ADAMS", names)
        self.assertEqual(len(result.rows), 8)

    def test_uncorrelated_exists_with_local_filter(self) -> None:
        result = execute(db(), "select d.deptno from dept d where exists "
                               "(select 1 from emp e where e.sal > 4000)")
        self.assertEqual(result.rows, [(10,), (20,), (30,), (40,)])

    def test_uncorrelated_exists_false(self) -> None:
        result = execute(db(), "select d.deptno from dept d where exists "
                               "(select 1 from emp e where e.sal > 9999)")
        self.assertEqual(result.rows, [])


class ScalarAggregateTests(unittest.TestCase):
    def test_group_join_count_zero_sum_null(self) -> None:
        result = execute(db(), """
            select d.deptno,
                   (select count(*) from emp e
                    where e.deptno = d.deptno) as c,
                   (select sum(e.sal) from emp e
                    where e.deptno = d.deptno) as s
            from dept d
        """)
        self.assertEqual(
            result.rows,
            [(10, 2, 7450), (20, 3, 6775), (30, 3, 3800), (40, 0, None)],
        )

    def test_empty_group_min_max_avg(self) -> None:
        result = execute(db(), """
            select d.deptno,
                   (select min(e.sal) from emp e
                    where e.deptno = d.deptno) as mn,
                   (select max(e.sal) from emp e
                    where e.deptno = d.deptno) as mx,
                   (select avg(e.sal) from emp e
                    where e.deptno = d.deptno) as av
            from dept d where d.deptno = 40
        """)
        self.assertEqual(result.rows, [(40, None, None, None)])

    def test_count_star_ignores_nulls_other_aggregates_skip_null(self) -> None:
        # SUM/MIN/MAX/AVG over comm: only ALLEN(300) and MARTIN(500) non-null.
        result = execute(db(), """
            select (select count(*) from emp e) as total_rows,
                   (select count(e.comm) from emp e) as non_null_comm,
                   (select sum(e.comm) from emp e) as comm_sum,
                   (select avg(e.comm) from emp e) as comm_avg
            from dept
            where deptno = 10
        """)
        self.assertEqual(result.rows, [(9, 2, 800, 400)])

    def test_arithmetic_around_aggregate_on_empty_group(self) -> None:
        result = execute(db(), """
            select d.deptno,
                   (select sum(e.sal) from emp e
                    where e.deptno = d.deptno) + 1 as plus_one
            from dept d where d.deptno = 40
        """)
        self.assertEqual(result.rows, [(40, None)])  # NULL + 1 == NULL

    def test_scalar_in_where(self) -> None:
        result = execute(db(), """
            select d.deptno from dept d
            where (select min(e.sal) from emp e
                   where e.deptno = d.deptno) > 2000
        """)
        self.assertEqual(result.rows, [(10,)])

    def test_uncorrelated_scalar(self) -> None:
        result = execute(db(),
            "select d.deptno, (select count(*) from emp) n from dept d "
            "where d.deptno = 20")
        self.assertEqual(result.rows, [(20, 9)])

    def test_outer_group_by_with_correlated_scalar(self) -> None:
        result = execute(db(), """
            select d.deptno,
                   (select count(*) from emp e
                    where e.deptno = d.deptno) c
            from dept d
            group by d.deptno
        """)
        self.assertEqual(result.rows,
                         [(10, 2), (20, 3), (30, 3), (40, 0)])

    def test_null_group_in_outer_group_by(self) -> None:
        result = execute(db(),
            "select deptno, count(*) from emp group by deptno")
        self.assertIn((None, 1), result.rows)


class ScopeShadowingTests(unittest.TestCase):
    def setUp(self) -> None:
        self.catalog = Database([Table("t", ("id", "pid"),
                                       [(1, None), (2, 1), (3, 1), (4, 2)])])

    def test_bare_column_binds_nearest_scope(self) -> None:
        # Inner `id` shadows nothing, but a bare predicate comparing inner pid
        # to a bare `id` resolves `id` to the INNER row (i.id), never outer.
        result = execute(self.catalog, """
            select o.id from t o
            where exists (select 1 from t i where i.pid = id)
        """)
        self.assertEqual(result.rows, [])

    def test_qualified_column_reaches_outer_scope(self) -> None:
        result = execute(self.catalog, """
            select o.id from t o
            where exists (select 1 from t i where i.pid = o.id)
        """)
        self.assertEqual(result.rows, [(1,), (2,)])

    def test_duplicate_alias_shadows_outer_qualified_name(self) -> None:
        # Both levels expose alias `o`; `o.pid = o.id` binds to the NEAREST `o`.
        result = execute(self.catalog, """
            select o.id from t o
            where exists (select 1 from t o
                          where o.pid = o.id and o.pid is not null)
        """)
        self.assertEqual(result.rows, [])


class ThreeValuedLogicTests(unittest.TestCase):
    def setUp(self) -> None:
        self.catalog = Database([Table("vals", ("a",), [(1,), (None,)])])

    def test_where_treats_null_as_false(self) -> None:
        result = execute(self.catalog,
            "select a from vals where a = 1")
        self.assertEqual(result.rows, [(1,)])

    def test_null_comparisons_in_and_or_not(self) -> None:
        rows = execute(self.catalog,
            "select a from vals where a = 1 or a is null").rows
        self.assertEqual(rows, [(1,), (None,)])
        rows = execute(self.catalog,
            "select a from vals where not (a = 1)").rows
        self.assertEqual(rows, [])  # NOT NULL == NULL -> filtered out

    def test_is_null_predicates(self) -> None:
        self.assertEqual(
            execute(self.catalog, "select a from vals where a is null").rows,
            [(None,)])
        self.assertEqual(
            execute(self.catalog,
                    "select a from vals where a is not null").rows, [(1,)])

    def test_arithmetic_null_propagation(self) -> None:
        self.assertEqual(
            execute(self.catalog,
                    "select a + 1 from vals where a is null").rows,
            [(None,)])

    def test_division_by_zero_becomes_null(self) -> None:
        self.assertEqual(
            execute(Database([Table("z", ("x",), [(0,)])]),
                    "select 10 / x from z").rows,
            [(None,)])


class RejectionTests(unittest.TestCase):
    def assertRejected(self, sql: str) -> None:
        with self.assertRaises(MiniSQLError):
            execute(db(), sql)

    def test_join_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from dept d join emp e on e.deptno = d.deptno")

    def test_non_equality_correlation_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from dept d where exists "
            "(select 1 from emp e where e.sal > d.deptno)")

    def test_correlation_disguised_in_or_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from dept d where exists "
            "(select 1 from emp e where e.deptno = d.deptno "
            " or e.sal = 1)")

    def test_expression_correlation_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from dept d where exists "
            "(select 1 from emp e where e.deptno + 0 = d.deptno)")

    def test_nested_subquery_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from dept d where exists "
            "(select 1 from emp e where e.deptno = d.deptno "
            "and exists (select 1 from dept x where x.deptno = e.deptno))")

    def test_non_aggregate_scalar_subquery_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select d.deptno, (select e.ename from emp e "
            "where e.empno = 1) from dept d")

    def test_subquery_group_by_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select d.deptno, (select count(*) from emp e "
            "where e.deptno = d.deptno group by e.deptno) from dept d")

    def test_subquery_in_from_rejected(self) -> None:
        self.assertRaises(MiniSQLError, execute, db(),
            "select x.deptno from (select deptno from emp) x")

    def test_aggregate_directly_in_where_is_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from emp where count(*) > 1")

    def test_scalar_aggregate_subquery_in_where_is_supported(self) -> None:
        result = execute(db(),
            "select ename from emp where sal > (select avg(sal) from emp)")
        self.assertEqual(result.rows,
                         [("CLARK",), ("KING",), ("JONES",), ("FORD",)])

    def test_exists_with_aggregate_body_rejected(self) -> None:
        with self.assertRaises(UnsupportedSQLError):
            execute(db(),
                "select * from dept d where exists "
                "(select count(*) from emp e where e.deptno = d.deptno)")
        with self.assertRaises(UnsupportedSQLError):
            execute(db(),
                "select * from dept d where exists "
                "(select 1 from emp e where e.deptno = d.deptno "
                "group by e.deptno)")

    def test_order_by_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select * from emp order by sal")

    def test_union_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select deptno from dept union select deptno from emp")

    def test_distinct_aggregate_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select count(distinct deptno) from emp")

    def test_unknown_function_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select coalesce(sal, 0) from emp")

    def test_bad_number_literal_is_syntax_error(self) -> None:
        self.assertRaises(SQLSyntaxError, execute, db(),
            "select 1. from emp")

    def test_unterminated_string_is_syntax_error(self) -> None:
        self.assertRaises(SQLSyntaxError, execute, db(),
            "select 'abc from emp")

    def test_bare_star_mixed_with_items_is_syntax_error(self) -> None:
        self.assertRaises(SQLSyntaxError, execute, db(),
            "select *, deptno from dept")

    def test_having_requires_group_by_and_is_rejected(self) -> None:
        self.assertRaises(UnsupportedSQLError, execute, db(),
            "select deptno from emp group by deptno having count(*) > 1")

    def test_non_grouped_column_is_semantic_error(self) -> None:
        self.assertRaises(SQLSemanticError, execute, db(),
            "select deptno, count(*) from emp")

    def test_unknown_table_and_column(self) -> None:
        self.assertRaises(SQLSemanticError, execute, db(),
            "select * from nope")
        self.assertRaises(SQLSemanticError, execute, db(),
            "select nope from emp")

    def test_ambiguous_column(self) -> None:
        self.assertRaises(SQLSemanticError, execute, db(),
            "select deptno from dept, emp")


if __name__ == "__main__":
    unittest.main(verbosity=2)


class ExtraSemanticsTests(unittest.TestCase):
    def test_multi_column_correlation(self) -> None:
        catalog = Database([
            Table("orders", ("oid", "cust", "region"), [
                (1, "ALICE", "EAST"), (2, "ALICE", "WEST"),
                (3, "BOB", "EAST"), (4, "BOB", "EAST")]),
            Table("ship", ("cust", "region", "ok"), [
                ("ALICE", "EAST", True), ("BOB", "EAST", False)]),
        ])
        result = execute(catalog, """
            select o.oid from orders o
            where exists (select 1 from ship s
                          where s.cust = o.cust and s.region = o.region
                            and s.ok = true)
        """)
        self.assertEqual(result.rows, [(1,)])

    def test_scalar_group_join_multikey(self) -> None:
        catalog = Database([
            Table("a", ("x", "y"), [(1, 1), (1, 2), (2, 1)]),
            Table("b", ("x", "y", "v"), [(1, 1, 10), (1, 1, 20),
                                         (1, 2, 30)]),
        ])
        result = execute(catalog, """
            select a.x, a.y,
                   (select sum(b.v) from b
                    where b.x = a.x and b.y = a.y) as total
            from a
        """)
        self.assertEqual(result.rows, [(1, 1, 30), (1, 2, 30), (2, 1, None)])

    def test_cartesian_from_then_exists_multiset(self) -> None:
        catalog = Database([
            Table("d", ("deptno",), [(10,), (20,)]),
            Table("e", ("empno", "deptno"), [(1, 10), (2, 10), (3, 20)]),
            Table("f", ("flag",), [(True,)]),
        ])
        # EXISTS must still yield one row per (d x f) outer multiset member,
        # regardless of how many employees match.
        result = execute(catalog, """
            select d.deptno, f.flag from d, f
            where exists (select 1 from e where e.deptno = d.deptno)
        """)
        self.assertEqual(result.rows, [(10, True), (20, True)])

    def test_count_column_versus_count_star(self) -> None:
        catalog = Database([Table("t", ("x",), [
            (1,), (None,), (2,), (None,)])])
        result = execute(catalog,
            "select count(*), count(x), sum(x), min(x), max(x) from t")
        self.assertEqual(result.rows, [(4, 2, 3, 1, 2)])

    def test_string_literal_predicate_and_output(self) -> None:
        result = execute(db(),
            "select ename, 'X' from emp where ename = 'KING'")
        self.assertEqual(result.rows, [("KING", "X")])
        # doubled quote inside a string
        result = execute(db(),
            "select 'it''s' from dept where deptno = 10")
        self.assertEqual(result.rows, [("it's",)])

    def test_select_star_expands_all_columns(self) -> None:
        result = execute(db(), "select * from dept where deptno = 40")
        self.assertEqual(result.columns, ("deptno", "dname"))
        self.assertEqual(result.rows, [(40, "OPERATIONS")])

    def test_grouped_query_empty_input_yields_no_rows(self) -> None:
        result = execute(db(),
            "select deptno, count(*) from emp where deptno = 99 "
            "group by deptno")
        self.assertEqual(result.rows, [])

    def test_ungrouped_aggregate_empty_input_yields_one_row(self) -> None:
        result = execute(db(),
            "select count(*), max(sal) from emp where deptno = 99")
        self.assertEqual(result.rows, [(0, None)])

    def test_exploratory_comparison_returns_null_filters_both_ways(self) -> None:
        catalog = Database([Table("t", ("x",), [(None,)])])
        self.assertEqual(execute(catalog,
            "select x from t where x <> 1").rows, [])
        self.assertEqual(execute(catalog,
            "select x from t where x = 1").rows, [])
