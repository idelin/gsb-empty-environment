"""Synthesised local sample tables (no external DB, no SQL engine).

employees(name, dept, age, salary)   -- salary/dept may be NULL
departments(dept, building)          -- building may be NULL
"""

from ivm import NULL

EMPLOYEE_SCHEMA = ("name", "dept", "age", "salary")
DEPARTMENT_SCHEMA = ("dept", "building")

# (row_id, row) pairs; row_id is the stable identity used by insert/delete.
DEPARTMENTS = [
    ("d1", ("eng", "A")),
    ("d2", ("ops", "B")),
    ("d3", ("hr", NULL)),      # NULL building
]

EMPLOYEES = [
    ("e1", ("amy", "eng", 34, 100)),
    ("e2", ("bob", "eng", 28, 90)),    # age < 30: filtered out of the view
    ("e3", ("cat", "ops", 41, 80)),
    ("e4", ("dan", NULL, 37, 70)),     # NULL dept: never joins
]

# Extra employees inserted later, as a second batch.
EMPLOYEES_BATCH2 = [
    ("e5", ("eli", "eng", 45, NULL)),  # NULL salary: ignored by SUM
    ("e6", ("flo", "ops", 41, 80)),    # duplicates cat's projected tuple
]
