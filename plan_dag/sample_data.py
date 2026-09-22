"""Synthetic local relations used by the demo and tests.

* duplicates are intentional (multiset semantics);
* ``NULL`` values are intentional (three-valued logic / outer join).
"""

from plan_dag.catalog import Catalog, Table
from plan_dag.datatypes import BOOLEAN, DOUBLE, INTEGER, TEXT, Field, Schema

EMPLOYEE_SCHEMA = Schema(
    [
        Field("id", INTEGER),
        Field("name", TEXT),
        Field("dept", INTEGER),
        Field("salary", INTEGER),
        Field("active", BOOLEAN),
    ]
)

EMPLOYEE_ROWS = [
    [1, "Ann", 10, 100, True],
    [2, "Bob", 10, 60, False],
    [3, "Cy", 20, 200, True],
    [4, "Di", 20, 60, None],
    [5, "Eli", None, 80, True],
    [6, "Fay", 30, None, True],
    [2, "Bob", 10, 60, False],  # exact duplicate row -> bag semantics
]

DEPARTMENT_SCHEMA = Schema(
    [Field("dept", INTEGER), Field("dname", TEXT), Field("budget", DOUBLE)]
)

DEPARTMENT_ROWS = [
    [10, "Eng", 1000.0],
    [20, "Sales", 500.0],
    [40, "Ops", 250.0],
]


def build_catalog() -> Catalog:
    return Catalog(
        [
            Table("employees", EMPLOYEE_SCHEMA, EMPLOYEE_ROWS),  # type: ignore[arg-type]
            Table("departments", DEPARTMENT_SCHEMA, DEPARTMENT_ROWS),  # type: ignore[arg-type]
        ]
    )
