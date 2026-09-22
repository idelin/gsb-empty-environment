class MiniSQLError(Exception):
    """Base class for all mini-SQL errors."""


class SQLSyntaxError(MiniSQLError):
    """The SQL text cannot be parsed inside the supported grammar."""


class UnsupportedSQLError(MiniSQLError):
    """The construct is syntactically recognizable but outside the supported subset."""


class SQLSemanticError(MiniSQLError):
    """Name resolution, typing, or usage rule violation."""
