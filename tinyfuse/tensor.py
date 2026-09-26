from __future__ import annotations

from collections.abc import Iterable, Sequence
from typing import Any

from .alloc import AllocationStats, allocator

current_stats: AllocationStats | None = None


def prod(values: Sequence[int]) -> int:
    result = 1
    for value in values:
        result *= value
    return result


def broadcast_shape(left: tuple[int, ...], right: tuple[int, ...]) -> tuple[int, ...]:
    result: list[int] = []
    for a, b in zip(
        (1,) * (len(right) - len(left)) + tuple(left),
        (1,) * (len(left) - len(right)) + tuple(right),
        strict=True,
    ):
        if a == b or a == 1:
            result.append(b)
        elif b == 1:
            result.append(a)
        else:
            raise ValueError(f"shapes cannot broadcast: {left} and {right}")
    return tuple(result)


def _record_allocation(size: int, dtype: str, reason: str) -> None:
    if current_stats is not None:
        current_stats.record(size, dtype, reason)


class Tensor:
    def __init__(
        self,
        data: Iterable[Any] | int | float | bool,
        shape: tuple[int, ...] | None = None,
        *,
        dtype: str | None = None,
        _storage: list[Any] | None = None,
        _offset: int = 0,
        _strides: tuple[int, ...] | None = None,
        _storage_id: int | None = None,
        _version: int = 0,
        _base: "Tensor | None" = None,
        _reason: str = "tensor",
    ) -> None:
        if isinstance(data, (int, float, bool)):
            values = [data]
            inferred_shape = shape or ()
        elif _storage is not None:
            values = []
            inferred_shape = shape or ()
        else:
            values = list(_flatten(data))
            inferred_shape = shape or tuple(_nested_shape(data))
        if shape is not None and shape != inferred_shape:
            if prod(shape) != (len(values) if _storage is None else prod(inferred_shape)):
                raise ValueError("shape does not match number of values")
            inferred_shape = shape
        self.shape = inferred_shape
        self.dtype = dtype or ("bool" if all(isinstance(v, bool) for v in values) else "float")
        if _storage is None:
            needed = prod(self.shape)
            if len(values) != needed:
                raise ValueError(f"expected {needed} values, got {len(values)}")
            self._storage = [bool(v) if self.dtype == "bool" else float(v) for v in values]
            self._offset = 0
            self._storage_id = allocator.new_storage_id()
            self._version = 0
            self._base = None
            _record_allocation(needed, self.dtype, _reason)
        else:
            if _strides is None:
                raise ValueError("internal storage requires strides")
            self._storage = _storage
            self._offset = _offset
            self._storage_id = allocator.new_storage_id() if _storage_id is None else _storage_id
            self._version = _version
            self._base = _base
        self._strides = _strides or tuple(
            prod(self.shape[index + 1 :]) for index in range(len(self.shape))
        )

    @property
    def ndim(self) -> int:
        return len(self.shape)

    @property
    def size(self) -> int:
        return prod(self.shape)

    @property
    def version(self) -> int:
        return self._version

    def storage_id(self) -> int:
        return self._storage_id

    def storage_alias_base(self) -> "Tensor":
        return self._base or self

    def storage_offset(self) -> int:
        return self._offset

    def strides(self) -> tuple[int, ...]:
        return self._strides

    def _linear_index(self, index: tuple[int, ...]) -> int:
        if len(index) != self.ndim:
            raise IndexError(f"expected {self.ndim} indices, got {len(index)}")
        return self._offset + sum(stride * coord for stride, coord in zip(self._strides, index))

    def _flat_offset(self, flat_index: int) -> int:
        offset = self._offset
        remainder = flat_index
        for dim, stride in zip(reversed(self.shape), reversed(self._strides), strict=True):
            offset += (remainder % dim) * stride
            remainder //= dim
        return offset

    def _broadcasted_index(self, index: tuple[int, ...]) -> int:
        pad = len(index) - self.ndim
        adjusted = tuple(0 if dim == 1 else coord for dim, coord in zip(self.shape, index[pad:]))
        return self._linear_index(adjusted)

    def __getitem__(self, index: tuple[int, ...] | int) -> Any:
        if not isinstance(index, tuple):
            index = (index,)
        return self._storage[self._linear_index(index)]

    def values(self) -> list[Any]:
        if self.ndim == 0:
            return [self._storage[self._offset]]
        return [self._storage[self._flat_offset(index)] for index in range(self.size)]

    def item(self) -> Any:
        if self.shape != ():
            raise ValueError("item only supports scalar tensors")
        return self._storage[self._offset]

    def clone(self, reason: str = "clone") -> "Tensor":
        _record_allocation(self.size, self.dtype, reason)
        return Tensor(
            self.values(),
            self.shape,
            dtype=self.dtype,
            _reason=reason,
        )

    def reshape(self, shape: int | tuple[int, ...]) -> "Tensor":
        if isinstance(shape, int):
            shape = (shape,)
        if prod(shape) != self.size:
            raise ValueError(f"cannot reshape {self.shape} to {shape}")
        if self._is_dense():
            return self._view(shape, tuple(prod(shape[i + 1 :]) for i in range(len(shape))), 0)
        return self.clone("reshape-contiguous")

    def transpose(self, dim0: int, dim1: int) -> "Tensor":
        if self.ndim < 2:
            raise ValueError("transpose requires at least two dimensions")
        a, b = dim0 % self.ndim, dim1 % self.ndim
        strides = list(self._strides)
        strides[a], strides[b] = strides[b], strides[a]
        return self._view(tuple(self.shape), tuple(strides), 0)

    def broadcast_to(self, shape: tuple[int, ...]) -> "Tensor":
        target = shape
        if len(target) < self.ndim:
            raise ValueError("cannot remove dimensions during broadcast")
        padded_shape = (1,) * (len(target) - self.ndim) + tuple(self.shape)
        for actual, wanted in zip(padded_shape, target, strict=True):
            if actual != wanted and actual != 1:
                raise ValueError(f"cannot broadcast {self.shape} to {shape}")
        strides = (0,) * (len(target) - self.ndim) + tuple(
            0 if dim == 1 else stride for dim, stride in zip(self.shape, self._strides)
        )
        return self._view(target, strides, 0)

    def _is_dense(self) -> bool:
        if self._offset != 0:
            return False
        expected = 1
        for dim, stride in zip(reversed(self.shape), reversed(self._strides), strict=True):
            if dim != 1 and stride != expected:
                return False
            expected *= dim
        return True

    def _view(self, shape: tuple[int, ...], strides: tuple[int, ...], offset: int) -> "Tensor":
        return Tensor(
            [],
            shape,
            dtype=self.dtype,
            _storage=self._storage,
            _offset=self._offset + offset,
            _strides=strides,
            _storage_id=self._storage_id,
            _version=self._version,
            _base=self.storage_alias_base(),
        )

    def may_alias(self, other: "Tensor") -> bool:
        if self._storage_id != other._storage_id:
            return False
        own_indices = {self._flat_offset(i) - self._offset for i in range(min(self.size, 1))}
        other_indices = {
            other._flat_offset(i) - other._offset for i in range(min(other.size, 1))
        }
        own_set = {self._flat_offset(i) for i in range(self.size)}
        other_set = {other._flat_offset(i) for i in range(other.size)}
        return bool(own_set & other_set) and bool(own_indices or other_indices or True)

    def fill_(self, value: Any) -> "Tensor":
        for index in range(self.size):
            self._storage[self._flat_offset(index)] = (
                bool(value) if self.dtype == "bool" else float(value)
            )
        self._version += 1
        return self

    def copy_(self, source: "Tensor") -> "Tensor":
        shape = broadcast_shape(self.shape, source.shape)
        if shape != self.shape:
            raise ValueError("copy destination must contain broadcast shape")
        if source._storage_id == self._storage_id and source.may_alias(self):
            source = source.clone("copy-alias-temp")
            self._version += 1
            copied = source
            copied.fill_(copied.item()) if copied.shape == () else None
        for flat_index in range(self.size):
            index = _unravel(flat_index, self.shape)
            self._storage[self._linear_index(index)] = source._storage[
                source._broadcasted_index(index)
            ]
        self._version += 1
        return self

    def add_(self, other: "Tensor") -> "Tensor":
        if other._storage_id == self._storage_id and other.may_alias(self):
            raise ValueError("refusing in-place add on overlapping views")
        if broadcast_shape(self.shape, other.shape) != self.shape:
            raise ValueError("in-place add destination must contain broadcast shape")
        for flat_index in range(self.size):
            index = _unravel(flat_index, self.shape)
            self._storage[self._linear_index(index)] += other._storage[
                other._broadcasted_index(index)
            ]
        self._version += 1
        return self


def _flatten(values: Iterable[Any]) -> Iterable[Any]:
    if isinstance(values, Tensor):
        yield from values.values()
    elif isinstance(values, Iterable) and not isinstance(values, (str, bytes)):
        for value in values:
            yield from _flatten(value)
    else:
        yield values


def _nested_shape(values: Any) -> tuple[int, ...]:
    if isinstance(values, Tensor):
        return values.shape
    if not isinstance(values, Iterable) or isinstance(values, (str, bytes)):
        return ()
    items = list(values)
    if not items:
        return (0,)
    child_shapes = {_nested_shape(item) for item in items}
    if len(child_shapes) != 1:
        raise ValueError("ragged nested sequences are not supported")
    return (len(items),) + child_shapes.pop()


def _unravel(flat_index: int, shape: tuple[int, ...]) -> tuple[int, ...]:
    result: list[int] = []
    remainder = flat_index
    for dim in reversed(shape):
        result.append(remainder % dim)
        remainder //= dim
    return tuple(reversed(result))
