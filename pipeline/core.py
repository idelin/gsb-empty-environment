"""Pull-based operator pipeline core (Python 3.11, stdlib only).

Contract
--------
- An operator is opened once with ``open()``, pulled repeatedly with
  ``next_batch(max_rows)``, and released with ``close()``.
- ``next_batch`` returns a ``list[Row]`` batch of at most ``max_rows`` rows,
  or ``None`` to signal EOF.  An *empty* batch is legal and does **not**
  mean EOF; callers must keep pulling.
- ``close()`` is idempotent (safe to call any number of times) and releases
  resources along the pipeline: every operator closes its children.
- If any ``next_batch`` call raises, the raising operator closes itself and
  its children before propagating, so a downstream failure releases all
  upstream resources.
- Cancellation is cooperative via :class:`CancelToken`; operators check the
  token once per batch and raise :class:`CancelledError`.
"""

from __future__ import annotations

import threading
from collections import deque
from typing import Callable, Optional

Row = tuple
Batch = list
Sink = Callable[[Batch], None]


class CancelledError(Exception):
    """Raised when a pipeline is cancelled through its CancelToken."""


class CancelToken:
    """Thread-safe cooperative cancellation token shared by a pipeline."""

    def __init__(self) -> None:
        self._event = threading.Event()

    def cancel(self) -> None:
        self._event.set()

    @property
    def cancelled(self) -> bool:
        return self._event.is_set()

    def throw_if_cancelled(self) -> None:
        if self.cancelled:
            raise CancelledError("pipeline cancelled")


class Operator:
    """Base class for all pipeline operators."""

    def __init__(self, token: Optional[CancelToken] = None) -> None:
        self.token = token if token is not None else CancelToken()
        self._opened = False
        self._closed = False

    # -- lifecycle ---------------------------------------------------------
    def open(self) -> "Operator":
        if self._closed:
            raise RuntimeError(f"{type(self).__name__}: cannot reopen a closed operator")
        if not self._opened:
            self._opened = True
            self._on_open()
        return self

    def _on_open(self) -> None:
        """Subclass hook: acquire resources here."""

    def close(self) -> None:
        """Idempotent: repeated close is safe."""
        if self._closed:
            return
        self._closed = True
        try:
            self._on_close()
        finally:
            self._close_children()

    def _on_close(self) -> None:
        """Subclass hook: release own resources here."""

    def _close_children(self) -> None:
        """Subclass hook: close child operators here."""

    @property
    def closed(self) -> bool:
        return self._closed

    # -- pulling -----------------------------------------------------------
    def next_batch(self, max_rows: int) -> Optional[Batch]:
        raise NotImplementedError

    def _check_pullable(self) -> None:
        if not self._opened:
            raise RuntimeError(f"{type(self).__name__}: not opened")
        if self._closed:
            raise RuntimeError(f"{type(self).__name__}: already closed")
        self.token.throw_if_cancelled()

    # -- context manager sugar --------------------------------------------
    def __enter__(self) -> "Operator":
        return self.open()

    def __exit__(self, exc_type, exc, tb) -> None:
        self.close()


class UnaryOperator(Operator):
    """Base for operators with exactly one child.

    Provides ``_pull_rows``: a buffered pull that guarantees at most
    ``max_rows`` rows per call even if the child returns oversized
    batches (surplus rows are kept in a backlog, never dropped).
    """

    def __init__(self, child: Operator, token: Optional[CancelToken] = None) -> None:
        super().__init__(token if token is not None else child.token)
        self.child = child
        self._backlog: deque = deque()
        self._child_eof = False

    def _on_open(self) -> None:
        self.child.open()

    def _close_children(self) -> None:
        self.child.close()

    def _pull_child(self, max_rows: int) -> Optional[Batch]:
        """Pull from the child; on any error, release the whole subtree."""
        try:
            return self.child.next_batch(max_rows)
        except BaseException:
            self.close()
            raise

    def _pull_rows(self, max_rows: int) -> Optional[Batch]:
        """Return up to ``max_rows`` child rows; ``None`` only at EOF.

        An empty list means "no rows available right now" (the child
        signalled an empty batch); it is not EOF.
        """
        out: Batch = []
        while len(out) < max_rows:
            if self._backlog:
                out.append(self._backlog.popleft())
                continue
            if self._child_eof:
                break
            need = max_rows - len(out)
            batch = self._pull_child(need)
            if batch is None:
                self._child_eof = True
                break
            out.extend(batch[:need])
            if len(batch) > need:
                self._backlog.extend(batch[need:])
            if not batch:
                break  # propagate the child's empty-batch signal
        if not out and self._child_eof:
            return None
        return out


def run(root: Operator, sink: Optional[Sink] = None, batch_size: int = 1024) -> int:
    """Pull ``root`` to EOF (or failure), delivering batches to ``sink``.

    Always closes ``root`` -- and therefore the whole pipeline -- exactly
    once, on success, cancellation, or error.  Returns the number of rows
    delivered.
    """
    root.open()
    delivered = 0
    try:
        while True:
            batch = root.next_batch(batch_size)
            if batch is None:  # EOF (empty batch is NOT EOF)
                return delivered
            if batch:
                if sink is not None:
                    sink(batch)
                delivered += len(batch)
    finally:
        root.close()
