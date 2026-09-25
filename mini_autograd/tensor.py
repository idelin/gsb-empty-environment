"""Tiny CPU tensors: a flat :class:`Storage` buffer plus a shape."""

from __future__ import annotations

from .errors import InplaceError
from .kernels import numel
from .storage import Storage

import threading

# Gradient commits from different graphs (different per-graph locks) can land
# on the same shared leaf concurrently; serialize the read-modify-write here.
_COMMIT_LOCK = threading.Lock()


class Tensor:
    __slots__ = (
        "_storage",
        "shape",
        "requires_grad",
        "_grad",
        "_version",
        "grad_fn",
        "graph",
        "__weakref__",
    )

    def __init__(self, data, shape=None, requires_grad=False, _own_storage=None):
        if isinstance(data, (int, float)):
            data = [float(data)]
            shape = () if shape is None else shape
        else:
            flat = []

            def _flatten(seq):
                for item in seq:
                    if isinstance(item, (list, tuple)):
                        _flatten(item)
                    else:
                        flat.append(float(item))

            _flatten(data)
            data = flat
        if shape is None:
            shape = (len(data),)
        if numel(shape) != len(data):
            raise ValueError(f"shape {shape} does not match {len(data)} values")

        if _own_storage is not None:
            self._storage = _own_storage
            self._storage.retain()
        else:
            self._storage = Storage(data)
            self._storage.retain()

        self.shape = tuple(shape)
        self.requires_grad = bool(requires_grad)
        self._grad = None
        self._version = 0
        self.grad_fn = None
        self.graph = None

    # -- storage ---------------------------------------------------------
    @property
    def storage(self):
        return self._storage

    def share_storage(self):
        """Return an unrecorded alias sharing the same heap buffer."""
        alias = Tensor.__new__(Tensor)
        alias._storage = self._storage
        alias._storage.retain()
        alias.shape = self.shape
        alias.requires_grad = False
        alias._grad = None
        alias._version = 0
        alias.grad_fn = None
        alias.graph = None
        return alias

    @property
    def data(self):
        if not self._storage.alive:
            from .errors import StorageFreedError

            raise StorageFreedError(
                f"storage {self._storage.id} backing this tensor was recycled"
            )
        return self._storage.data

    # -- autograd metadata ----------------------------------------------
    @property
    def is_leaf(self):
        return self.grad_fn is None

    @property
    def grad(self):
        return self._grad

    @property
    def version(self):
        return self._version

    def zero_grad(self):
        self._grad = None

    def add_grad(self, values):
        """Commit a leaf gradient, accumulating across backward transactions."""
        with _COMMIT_LOCK:
            if self._grad is None:
                self._grad = Tensor(values, self.shape, requires_grad=False)
            else:
                self._grad._mutate_checked(
                    [a + b for a, b in zip(self._grad.data, values)]
                )

    # -- in-place mutation (the only sanctioned raw buffer writer) -------
    def _mutate_checked(self, new_data):
        """Write ``new_data`` into the buffer and bump the version counter.

        In-place writes are only legal on leaves (PyTorch semantics); mutating a
        saved input after forward is legal at the leaf level but is detected
        later during backward through the saved version counter.
        """
        if not self.is_leaf:
            raise InplaceError(
                "in-place modification of a non-leaf tensor is not supported"
            )
        if len(new_data) != len(self.data):
            raise ValueError("in-place writes must preserve size")
        self._storage.data[:] = (float(v) for v in new_data)
        self._version += 1

    def add_(self, values):
        values = [float(v) for v in getattr(values, "data", values)]
        self._mutate_checked([a + b for a, b in zip(self.data, values)])
        return self

    def copy_data(self, values):
        values = [float(v) for v in getattr(values, "data", values)]
        self._mutate_checked(values)

    # -- entry point -----------------------------------------------------
    def backward(self, gradient=None, retain_graph=False, cancel=None):
        from .engine import backward

        backward(self, gradient, retain_graph=retain_graph, cancel=cancel)

    # -- misc ------------------------------------------------------------
    def item(self):
        if len(self.data) != 1:
            raise ValueError("item() only valid for single-element tensors")
        return self.data[0]

    def detach(self):
        return Tensor(list(self.data), self.shape, requires_grad=False)

    def __repr__(self):
        if self.shape == ():
            body = repr(self.data[0])
        else:
            body = repr(self.data)
        return f"Tensor({body}, shape={self.shape}, requires_grad={self.requires_grad})"

    def __del__(self):
        storage = getattr(self, "_storage", None)
        if storage is not None:
            try:
                storage.release()
            except Exception:
                pass
