class Span:
    __slots__ = ("start", "end")

    def __init__(self, start, end):
        self.start = start
        self.end = end

    def __repr__(self):
        return f"Span({self.start}, {self.end})"


class StackLangError(Exception):
    def __init__(self, message, span=None):
        super().__init__(message)
        self.message = message
        self.span = span

    def __str__(self):
        if self.span is None:
            return self.message
        return f"{self.message} (bytes {self.span.start}..{self.span.end})"


class LexError(StackLangError):
    pass


class ParseError(StackLangError):
    pass


class CompileError(StackLangError):
    pass


class ValidationError(StackLangError):
    pass


class RuntimeErr(StackLangError):
    pass


class BudgetExceeded(RuntimeErr):
    def __init__(self, budget):
        super().__init__(f"instruction budget of {budget} exceeded", None)
        self.budget = budget
