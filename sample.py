#!/usr/bin/env python3
"""Runnable demonstration of the mini-SQL subquery rewriter.

Run:  python3 sample.py
No SQL engine is used; tables are local multisets and every operation is
executed directly in Python.
"""
from __future__ import annotations

from minisql import Database, Result, Table, execute


DEPT = Table("dept", ("deptno", "dname", "loc"), [
    (10, "ACCOUNTING", "NEW YORK"),
    (20, "RESEARCH", "DALLAS"),
    (30, "SALES", "CHICAGO"),
    (40, "OPERATIONS", "BOSTON"),
])

EMP = Table("emp", ("empno", "ename", "job", "deptno", "sal", "comm"), [
    (7782, "CLARK", "MANAGER", 10, 2450, None),
    (7839, "KING", "PRESIDENT", 10, 5000, None),
    (7369, "SMITH", "CLERK", 20, 800, None),
    (7566, "JONES", "MANAGER", 20, 2975, None),
    (7902, "FORD", "ANALYST", 20, 3000, None),
    (7499, "ALLEN", "SALESMAN", 30, 1600, 300),
    (7654, "MARTIN", "SALESMAN", 30, 1250, 500),
    (7900, "JAMES", "CLERK", 30, 950, None),
    (7876, "ADAMS", "CLERK", None, 1100, None),   # NULL department
])

DB = Database([DEPT, EMP])

QUERIES = [
    ("EXISTS -> semi-join (departments that have at least one employee)",
     """
     select d.deptno, d.dname
     from dept d
     where exists (select 1 from emp e where e.deptno = d.deptno)
     """),
    ("NOT EXISTS -> anti / empty semi-join (department with no employee)",
     """
     select d.deptno, d.dname
     from dept d
     where not exists (select * from emp e where e.deptno = d.deptno)
     """),
    ("Scalar aggregate subqueries -> group-join (COUNT 0, SUM NULL on no match)",
     """
     select d.deptno,
            (select count(*) from emp e where e.deptno = d.deptno) as headcount,
            (select sum(e.sal) from emp e where e.deptno = d.deptno) as payroll
     from dept d
     """),
    ("Scalar aggregate inside WHERE, with NULL-safe MIN over no-match groups",
     """
     select d.deptno, d.dname
     from dept d
     where (select min(e.sal)
            from emp e
            where e.deptno = d.deptno and e.comm is not null) >= 400
     """),
    ("Outer GROUP BY with a correlated COUNT per department",
     """
     select d.deptno,
            (select count(*) from emp e where e.deptno = d.deptno) as headcount
     from dept d
     where d.deptno < 40
     group by d.deptno
     """),
]


def show(title: str, sql: str, result: Result) -> None:
    print("=" * 72)
    print(title)
    print("-" * 72)
    print(" ".join(sql.split()))
    print("-" * 72)
    width = 14
    print(" | ".join(name.center(width) for name in result.columns))
    for row in result.rows:
        print(" | ".join(("NULL" if value is None else str(value)).center(width)
                         for value in row))
    print()


def main() -> None:
    for title, sql in QUERIES:
        show(title, sql, execute(DB, sql))


if __name__ == "__main__":
    main()
