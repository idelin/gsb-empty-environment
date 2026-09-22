class MiniSQLError(Exception):
    """Base class for all errors raised by the mini SQL implementation."""


class UnsupportedSQL(MiniSQLError):
    """Raised when input uses syntax outside the deliberately supported subset."""


class ParseError(MiniSQLError):
    pass


class BindError(MiniSQLError):
    pass


class TypeRuntimeError(MiniSQLError):
    pass
