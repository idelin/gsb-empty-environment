class LangError(Exception):
    """Error raised for lex/parse/runtime failures.

    start/end are UTF-8 byte offsets into the source, when known.
    """

    def __init__(self, message, start=None, end=None):
        super().__init__(message)
        self.message = message
        self.start = start
        self.end = end

    def __str__(self):
        if self.start is not None and self.end is not None:
            return f"{self.message} [bytes {self.start}..{self.end}]"
        return self.message
