class DictMergeError(Exception):
    """Base error for dictionary merge failures."""


class SchemaError(DictMergeError):
    pass


class InvalidDictionaryCodeError(DictMergeError):
    def __init__(self, *, batch_index, column, code, batch_row, global_row, version):
        self.batch_index = batch_index
        self.column = column
        self.code = code
        self.batch_row = batch_row
        self.global_row = global_row
        self.version = version
        super().__init__(
            f"invalid dictionary code {code!r} for column {column!r} "
            f"in batch {batch_index}, row {batch_row} (global row {global_row}); "
            f"dictionary version {version!r}"
        )


class WithdrawnTermError(DictMergeError):
    def __init__(self, *, batch_index, column, code, batch_row, global_row, version, term_id):
        self.batch_index = batch_index
        self.column = column
        self.code = code
        self.batch_row = batch_row
        self.global_row = global_row
        self.version = version
        self.term_id = term_id
        super().__init__(
            f"dictionary term {term_id!r} is withdrawn for column {column!r} "
            f"in batch {batch_index}, row {batch_row} (global row {global_row})"
        )


class DictionaryVersionError(DictMergeError):
    pass


class ChunkCoverageError(DictMergeError):
    pass


class StableTermConflictError(DictMergeError):
    pass


class MemoryLimitExceeded(DictMergeError):
    pass


class MergeCancelled(DictMergeError):
    pass
