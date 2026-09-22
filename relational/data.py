from __future__ import annotations

from .catalog import set_active_tables
from .relations import make_table


SCORE_ROWS = tuple(
    {"id": index, "score": 10 if index < 12 else (9 if index < 18 else 8), "label": f"p{partition}-{index}"}
    for partition in range(2)
    for index in range(partition * 10, partition * 10 + 10)
)


def sample_tables():
    scores_p0 = SCORE_ROWS[:10]
    scores_p1 = SCORE_ROWS[10:]
    departments = (
        {"id": 10, "region": "north", "active": True},
        {"id": 20, "region": "south", "active": False},
        {"id": 30, "region": "east", "active": True},
        {"id": 40, "region": "west", "active": True},
    )
    employees = (
        {"id": 1, "department_id": 10, "salary": 100.0, "commission": 10.0},
        {"id": 2, "department_id": 10, "salary": 200.0, "commission": None},
        {"id": 3, "department_id": 20, "salary": 50.0, "commission": 5.0},
        {"id": 4, "department_id": None, "salary": 75.0, "commission": None},
        {"id": 5, "department_id": 30, "salary": None, "commission": 2.0},
        {"id": 6, "department_id": 30, "salary": 300.0, "commission": 3.0},
        {"id": 6, "department_id": 30, "salary": 300.0, "commission": 3.0},
    )
    tables = {
        "scores": make_table(
            "scores",
            ("id", "score", "label"),
            (scores_p0, scores_p1),
            unique_columns=("id",),
        ),
        "departments": make_table(
            "departments",
            ("id", "region", "active"),
            (departments,),
            unique_columns=("id",),
        ),
        "employees": make_table(
            "employees",
            ("id", "department_id", "salary", "commission"),
            (employees,),
            unique_columns=(),
        ),
    }

    set_active_tables(tables)
    return tables
