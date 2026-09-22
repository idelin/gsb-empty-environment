"""Bounded, spillable merge join for two ascending keyed streams."""

from __future__ import annotations

import math
import os
import pickle
import tempfile
from collections.abc import Callable, Iterable, Iterator
from typing import Any


class MergeJoinError(Exception):
    """Base class for merge join failures."""


class UnsortedStreamError(MergeJoinError, ValueError):
    """Raised when an input stream violates ascending key order."""


class IncomparableKeysError(MergeJoinError, TypeError):
    """Raised when keys from the two streams cannot be ordered together."""


class InvalidJoinKeyError(MergeJoinError, ValueError):
    """Raised when a key cannot have a well-defined merge order."""


class JoinCancelledError(MergeJoinError):
    """Raised after a cancellation event is observed."""


GroupCallback = Callable[[dict[str, Any]], None]


def _contains_nan(value: Any) -> bool:
    if isinstance(value, tuple):
        return any(isinstance(part, float) and math.isnan(part) for part in value)
    return isinstance(value, float) and math.isnan(value)


def _is_null_key(value: Any) -> bool:
    if isinstance(value, tuple):
        return any(part is None for part in value)
    return value is None


class JoinKey:
    """Order wrapper with SQL NULL semantics; NULL sorts before non-NULL."""

    __slots__ = ("_key", "_is_null")

    def __init__(self, value: Any) -> None:
        if _contains_nan(value):
            raise InvalidJoinKeyError("NaN is not an ordered merge join key")
        self._key = value
        self._is_null = _is_null_key(value)

    @property
    def is_null(self) -> bool:
        return self._is_null

    @property
    def value(self) -> Any:
        return self._key

    def __eq__(self, other: object) -> bool:
        if not isinstance(other, JoinKey):
            return NotImplemented
        if self._is_null or other._is_null:
            return False
        return self._key == other._key

    def __lt__(self, other: object) -> bool:
        if not isinstance(other, JoinKey):
            return NotImplemented
        if self._is_null:
            return not other._is_null
        if other._is_null:
            return False
        try:
            return self._key < other._key
        except TypeError as exc:
            raise IncomparableKeysError(
                f"cannot order merge join keys {self._key!r} and {other._key!r}"
            ) from exc


def _as_key_selector(
    selector: Callable[[Any], Any] | int | str | None,
) -> Callable[[Any], Any]:
    if selector is None:
        return lambda row: row
    if callable(selector):
        return selector
    if isinstance(selector, int):
        return lambda row: row[selector]
    if isinstance(selector, str):
        return lambda row: row[selector]
    raise TypeError("key selector must be callable, an integer, a string, or None")


class OrderedStream:
    """Lazy ascending stream validator with one-row lookahead."""

    def __init__(
        self,
        rows: Iterable[Any],
        key: Callable[[Any], Any] | int | str | None,
        *,
        name: str,
        cancel_event: Any = None,
    ) -> None:
        self._rows = iter(rows)
        self._key_selector = _as_key_selector(key)
        self._name = name
        self._cancel_event = cancel_event
        self._last_key: JoinKey | None = None
        self._row: Any = None
        self._join_key: JoinKey | None = None
        self._exhausted = False

    @property
    def name(self) -> str:
        return self._name

    @property
    def current_row(self) -> Any:
        return self._row

    @property
    def current_key(self) -> JoinKey | None:
        return self._join_key

    @property
    def exhausted(self) -> bool:
        return self._exhausted

    def _check_cancelled(self) -> None:
        if self._cancel_event is not None and self._cancel_event.is_set():
            raise JoinCancelledError("merge join was cancelled")

    def advance(self) -> None:
        self._check_cancelled()
        try:
            row = next(self._rows)
        except StopIteration:
            self._row = None
            self._join_key = None
            self._exhausted = True
            return

        try:
            join_key = JoinKey(self._key_selector(row))
        except InvalidJoinKeyError:
            raise
        except Exception as exc:
            raise MergeJoinError(f"{self._name} key selector failed") from exc

        if self._last_key is not None:
            try:
                descending = join_key < self._last_key
            except IncomparableKeysError as exc:
                raise UnsortedStreamError(
                    f"{self._name} contains incomparable consecutive keys "
                    f"{self._last_key.value!r} and {join_key.value!r}"
                ) from exc
            if descending:
                raise UnsortedStreamError(
                    f"{self._name} is not sorted: {join_key.value!r} follows "
                    f"{self._last_key.value!r}"
                )

        self._last_key = join_key
        self._row = row
        self._join_key = join_key


class SpooledGroup:
    """In-memory group that spills to a pickle stream once a bound is reached."""

    def __init__(
        self,
        *,
        max_in_memory: int,
        spill_directory: str | None = None,
    ) -> None:
        if (
            not isinstance(max_in_memory, int)
            or isinstance(max_in_memory, bool)
            or max_in_memory < 0
        ):
            raise ValueError("max_in_memory must be a non-negative integer")
        self._max_in_memory = max_in_memory
        self._spill_directory = spill_directory
        self._memory: list[Any] = []
        self._file: Any = None
        self._path: str | None = None
        self._count = 0
        self._spilled = False

    @property
    def spilled(self) -> bool:
        return self._spilled

    @property
    def path(self) -> str | None:
        return self._path

    def __len__(self) -> int:
        return self._count

    def add(self, row: Any) -> None:
        if self._count < self._max_in_memory:
            self._memory.append(row)
        else:
            if not self._spilled:
                descriptor, path = tempfile.mkstemp(
                    prefix="merge_join_", suffix=".pkl", dir=self._spill_directory
                )
                self._path = path
                self._file = os.fdopen(descriptor, "w+b")
                for buffered_row in self._memory:
                    pickle.dump(buffered_row, self._file)
                self._memory.clear()
                self._spilled = True
            pickle.dump(row, self._file)
        self._count += 1

    def __iter__(self) -> Iterator[Any]:
        if not self._spilled:
            yield from self._memory
            return

        assert self._file is not None
        self._file.flush()
        self._file.seek(0)
        for _ in range(self._count):
            yield pickle.load(self._file)
        self._file.seek(0, os.SEEK_END)

    def close(self) -> None:
        if self._file is not None:
            self._file.close()
            self._file = None
        if self._path is not None:
            try:
                os.unlink(self._path)
            except FileNotFoundError:
                pass
            self._path = None

    def __enter__(self) -> SpooledGroup:
        return self

    def __exit__(self, exc_type: object, exc: object, traceback: object) -> None:
        self.close()


def merge_join(
    left_rows: Iterable[Any],
    right_rows: Iterable[Any],
    left_key: Callable[[Any], Any] | int | str | None = None,
    right_key: Callable[[Any], Any] | int | str | None = None,
    *,
    how: str = "inner",
    max_in_memory: int = 1000,
    spill_directory: str | None = None,
    cancel_event: Any = None,
    group_callback: GroupCallback | None = None,
) -> Iterator[tuple[Any, Any]]:
    """Merge join two ascending streams.

    ``how`` is ``"inner"`` or ``"left"``. Matches are ``(left_row, right_row)``.
    Unmatched left rows in a left join are ``(left_row, None)``.
    """

    if how not in ("inner", "left"):
        raise ValueError("how must be 'inner' or 'left'")
    if (
        not isinstance(max_in_memory, int)
        or isinstance(max_in_memory, bool)
        or max_in_memory < 0
    ):
        raise ValueError("max_in_memory must be a non-negative integer")
    if cancel_event is not None and not callable(getattr(cancel_event, "is_set", None)):
        raise TypeError("cancel_event must provide an is_set() method")
    if group_callback is not None and not callable(group_callback):
        raise TypeError("group_callback must be callable")

    left_selector = _as_key_selector(left_key)
    effective_right_key = left_key if right_key is None else right_key
    right_selector = _as_key_selector(effective_right_key)

    def joined() -> Iterator[tuple[Any, Any]]:
        yield from _merge_join(
            left_rows,
            right_rows,
            left_selector,
            right_selector,
            how=how,
            max_in_memory=max_in_memory,
            spill_directory=spill_directory,
            cancel_event=cancel_event,
            group_callback=group_callback,
        )

    return joined()


def _check_cancel(event: Any) -> None:
    if event is not None and event.is_set():
        raise JoinCancelledError("merge join was cancelled")


def _same_key(stream: OrderedStream, key: JoinKey) -> bool:
    current = stream.current_key
    return current is not None and not current.is_null and current == key


def _read_group(
    stream: OrderedStream,
    key: JoinKey,
    max_in_memory: int,
    spill_directory: str | None,
    event: Any,
) -> SpooledGroup:
    group = SpooledGroup(max_in_memory=max_in_memory, spill_directory=spill_directory)
    try:
        while (
            not stream.exhausted
            and _same_key(stream, key)
        ):
            _check_cancel(event)
            group.add(stream.current_row)
            stream.advance()
    except BaseException:
        group.close()
        raise
    return group


def _read_null_group(
    stream: OrderedStream,
    max_in_memory: int,
    spill_directory: str | None,
    event: Any,
) -> SpooledGroup:
    group = SpooledGroup(max_in_memory=max_in_memory, spill_directory=spill_directory)
    try:
        while (
            not stream.exhausted
            and stream.current_key is not None
            and stream.current_key.is_null
        ):
            _check_cancel(event)
            group.add(stream.current_row)
            stream.advance()
    except BaseException:
        group.close()
        raise
    return group


def _skip_group(stream: OrderedStream, key: JoinKey, event: Any) -> None:
    while not stream.exhausted and _same_key(stream, key):
        _check_cancel(event)
        stream.advance()


def _merge_join(
    left_rows: Iterable[Any],
    right_rows: Iterable[Any],
    left_selector: Callable[[Any], Any],
    right_selector: Callable[[Any], Any],
    *,
    how: str,
    max_in_memory: int,
    spill_directory: str | None,
    cancel_event: Any,
    group_callback: GroupCallback | None,
) -> Iterator[tuple[Any, Any]]:
    left = OrderedStream(
        left_rows, left_selector, name="left stream", cancel_event=cancel_event
    )
    right = OrderedStream(
        right_rows, right_selector, name="right stream", cancel_event=cancel_event
    )
    left.advance()
    right.advance()

    while not left.exhausted:
        _check_cancel(cancel_event)
        left_key = left.current_key
        assert left_key is not None

        if left_key.is_null:
            with _read_null_group(
                left, max_in_memory, spill_directory, cancel_event
            ) as null_group:
                if how == "left":
                    for null_row in null_group:
                        yield null_row, None
            continue

        if right.exhausted:
            if how == "left":
                while not left.exhausted:
                    _check_cancel(cancel_event)
                    current = left.current_key
                    assert current is not None
                    if current.is_null:
                        with _read_null_group(
                            left, max_in_memory, spill_directory, cancel_event
                        ) as null_group:
                            for null_row in null_group:
                                yield null_row, None
                    else:
                        with _read_group(
                            left,
                            current,
                            max_in_memory,
                            spill_directory,
                            cancel_event,
                        ) as unmatched_group:
                            for left_row in unmatched_group:
                                yield left_row, None
            return

        right_key = right.current_key
        assert right_key is not None

        if right_key.is_null:
            right.advance()
            continue

        if left_key < right_key:
            if how == "left":
                with _read_group(
                    left,
                    left_key,
                    max_in_memory,
                    spill_directory,
                    cancel_event,
                ) as unmatched_group:
                    for left_row in unmatched_group:
                        yield left_row, None
            else:
                _skip_group(left, left_key, cancel_event)
        elif right_key < left_key:
            _skip_group(right, right_key, cancel_event)
        else:
            with _read_group(
                left, left_key, max_in_memory, spill_directory, cancel_event
            ) as left_group, _read_group(
                right, right_key, max_in_memory, spill_directory, cancel_event
            ) as right_group:
                if group_callback is not None:
                    group_callback(
                        {
                            "key": left_key.value,
                            "left_count": len(left_group),
                            "right_count": len(right_group),
                            "left_spilled": left_group.spilled,
                            "right_spilled": right_group.spilled,
                            "left_spill_path": left_group.path,
                            "right_spill_path": right_group.path,
                        }
                    )
                for left_row in left_group:
                    for right_row in right_group:
                        _check_cancel(cancel_event)
                        yield left_row, right_row
