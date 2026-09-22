"""minisql: SQL subset executor with decorrelated correlated subqueries.

Supports:
  * SELECT ... FROM t[, t ...] [WHERE ...] [GROUP BY col, ...]
  * EXISTS / NOT EXISTS correlated subqueries  -> semi-join (no row duplication)
  * scalar aggregate correlated subqueries     -> group-join
  * aggregates COUNT/SUM/AVG/MIN/MAX, 3VL NULL logic, multiset semantics

Only the Python standard library is used; no SQL engine is invoked.
"""

from .engine import Database, Table, execute, MiniSQLError, SQLSyntaxError
from .engine import UnsupportedSQLError, SQLSemanticError, Result

__all__ = [
    "Database",
    "Table",
    "execute",
    "Result",
    "MiniSQLError",
    "SQLSyntaxError",
    "UnsupportedSQLError",
    "SQLSemanticError",
]
