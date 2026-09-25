from dataclasses import dataclass
import math
import random

from .encoding import check_value, values_equal
from .errors import (
    DictionaryVersionError,
    InvalidDictionaryCodeError,
    SchemaError,
    WithdrawnTermError,
)


class CollisionSafeMap:
    """Identity map that remains correct when hashes are forced to collide."""

    def __init__(self, hash_func=hash, minimum_buckets=4):
        self._hash = hash_func
        self._minimum_buckets = max(1, minimum_buckets)
        self._buckets = [[] for _ in range(self._minimum_buckets)]
        self._size = 0

    def __len__(self):
        return self._size

    def __contains__(self, key):
        return self._entry(key) is not None

    def _entry(self, key):
        index = self._hash(key) % len(self._buckets)
        for entry in self._buckets[index]:
            if entry[0] == key:
                return entry
        return None

    def get(self, key, default=None):
        entry = self._entry(key)
        return default if entry is None else entry[1]

    def put(self, key, value):
        entry = self._entry(key)
        if entry is not None:
            entry[1] = value
            return
        self._buckets[self._hash(key) % len(self._buckets)].append([key, value])
        self._size += 1
        if self._size * 3 >= len(self._buckets) * 2:
            self._resize(len(self._buckets) * 2)

    def _resize(self, size):
        old = self._buckets
        self._buckets = [[] for _ in range(size)]
        for bucket in old:
            for key, value in bucket:
                self._buckets[self._hash(key) % size].append([key, value])

    def items(self):
        for bucket in self._buckets:
            for key, value in bucket:
                yield key, value


@dataclass(frozen=True)
class TermSpec:
    term_id: str
    value: object
    withdrawn: bool = False


@dataclass(frozen=True)
class Column:
    name: str
    value_type: str


@dataclass(frozen=True)
class EncodedColumn:
    column: Column
    codes: tuple[object, ...]

    def __post_init__(self):
        for code in self.codes:
            if code is not None and (not isinstance(code, int) or isinstance(code, bool) or code < 0):
                raise SchemaError("dictionary codes must be non-negative integers or None")

    @property
    def row_count(self):
        return len(self.codes)


@dataclass(frozen=True)
class EncodedChunk:
    batch_index: int
    start_row: int
    columns: tuple[EncodedColumn, ...]

    @property
    def row_count(self):
        return len(self.columns[0].codes) if self.columns else 0


@dataclass(frozen=True)
class DictBatch:
    batch_index: int
    dictionaries: dict[str, "LocalDictionary"]
    columns: tuple[EncodedColumn, ...]

    def __post_init__(self):
        if not self.columns:
            raise SchemaError("a batch must contain at least one column")
        rows = {column.row_count for column in self.columns}
        if len(rows) != 1:
            raise SchemaError("all columns in a batch must have the same row count")
        for column in self.columns:
            if column.column.name not in self.dictionaries:
                raise SchemaError(f"missing dictionary for column {column.column.name!r}")

    @property
    def row_count(self):
        return self.columns[0].row_count

    def dictionary(self, column_name):
        return self.dictionaries[column_name]


class LocalDictionary:
    def __init__(self, version, terms_by_column, *, hash_func=hash):
        if not isinstance(version, str) or not version:
            raise ValueError("dictionary version must be a non-empty string")
        self.version = version
        self.terms_by_column = {}
        self._indexes = {}
        for column, terms in terms_by_column.items():
            name = column.name if isinstance(column, Column) else column
            normalized = tuple(terms)
            index = CollisionSafeMap(hash_func=hash_func)
            for code, term in enumerate(normalized):
                if not isinstance(term, TermSpec):
                    raise TypeError("dictionary entries must be TermSpec instances")
                value_type = column.value_type if isinstance(column, Column) else "string"
                check_value(value_type, term.value)
                if term.term_id in index:
                    raise ValueError(f"duplicate stable term id {term.term_id!r}")
                index.put(term.term_id, code)
            self.terms_by_column[name] = normalized
            self._indexes[name] = index

    def columns(self):
        return tuple(self.terms_by_column)

    def terms(self, column_name):
        return self.terms_by_column[column_name]

    def code_for(self, column_name, term_id):
        return self._indexes[column_name].get(term_id)

    def term_for_code(self, column_name, code, *, version=None):
        self._require_version(version)
        terms = self.terms_by_column[column_name]
        if not isinstance(code, int) or isinstance(code, bool) or code < 0 or code >= len(terms):
            raise InvalidDictionaryCodeError(
                batch_index=-1,
                column=column_name,
                code=code,
                batch_row=-1,
                global_row=-1,
                version=self.version,
            )
        term = terms[code]
        if term.withdrawn:
            raise WithdrawnTermError(
                batch_index=-1,
                column=column_name,
                code=code,
                batch_row=-1,
                global_row=-1,
                version=self.version,
                term_id=term.term_id,
            )
        return term

    def decode_code(self, column_name, code, *, version=None):
        return self.term_for_code(column_name, code, version=version).value

    def _require_version(self, version):
        if version is not None and version != self.version:
            raise DictionaryVersionError(
                f"dictionary code requires version {version!r}, not {self.version!r}"
            )

    def withdrawn_copy(self, withdrawals, new_version):
        if not isinstance(withdrawals, dict):
            withdrawals = {column_name: frozenset(withdrawals) for column_name in self.terms_by_column}
        result = {}
        for column_name, terms in self.terms_by_column.items():
            removed = withdrawals.get(column_name, frozenset())
            result[column_name] = tuple(
                TermSpec(term.term_id, term.value, True) if term.term_id in removed else term
                for term in terms
            )
        return LocalDictionary(new_version, result)

    def assert_same_term(self, column_name, other, term_id):
        left_code = self.code_for(column_name, term_id)
        right_code = other.code_for(column_name, term_id)
        if left_code is None or right_code is None:
            return False
        left = self.terms_by_column[column_name][left_code]
        right = other.terms_by_column[column_name][right_code]
        return values_equal("string", left.value, right.value)


def decode_with_dictionary(batch, *, column_names=None):
    names = column_names if column_names is not None else [column.column.name for column in batch.columns]
    result = {}
    for encoded in batch.columns:
        if encoded.column.name not in names:
            continue
        dictionary = batch.dictionary(encoded.column.name)
        values = []
        for batch_row, code in enumerate(encoded.codes):
            if code is None:
                values.append(None)
                continue
            try:
                values.append(
                    dictionary.decode_code(
                        encoded.column.name,
                        code,
                        version=dictionary.version,
                    )
                )
            except InvalidDictionaryCodeError as exc:
                raise InvalidDictionaryCodeError(
                    batch_index=batch.batch_index,
                    column=encoded.column.name,
                    code=code,
                    batch_row=batch_row + 1,
                    global_row=batch_row + 1,
                    version=dictionary.version,
                ) from exc
            except WithdrawnTermError as exc:
                term = dictionary.terms(encoded.column.name)[code]
                raise WithdrawnTermError(
                    batch_index=batch.batch_index,
                    column=encoded.column.name,
                    code=code,
                    batch_row=batch_row + 1,
                    global_row=batch_row + 1,
                    version=dictionary.version,
                    term_id=term.term_id,
                ) from exc
        result[encoded.column.name] = tuple(values)
    return result


def split_batch_random(batch, *, seed=0, shuffle=True, minimum_chunks=1):
    if minimum_chunks < 1:
        raise ValueError("minimum_chunks must be positive")
    rng = random.Random(seed)
    starts = [0]
    while starts[-1] < batch.row_count:
        remaining = batch.row_count - starts[-1]
        advance = rng.randint(1, max(1, math.ceil(remaining / 2)))
        starts.append(min(batch.row_count, starts[-1] + advance))
    if len(starts) - 1 < minimum_chunks and batch.row_count >= minimum_chunks:
        starts = sorted({0, batch.row_count, *rng.sample(range(1, batch.row_count), minimum_chunks - 1)})
    chunks = []
    for start, end in zip(starts, starts[1:]):
        columns = tuple(
            EncodedColumn(column.column, tuple(column.codes[start:end]))
            for column in batch.columns
        )
        chunks.append(EncodedChunk(batch.batch_index, start, columns))
    if shuffle:
        rng.shuffle(chunks)
    return chunks
