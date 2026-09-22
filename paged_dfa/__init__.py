"""Paged compressed transition tables for large DFAs.

A DFA transition table is compressed with:

* a per-row *default destination*,
* *interval exceptions* (runs of symbols that depart from the default),
* *row sharing* (identical compressed rows share one row id),
* *paging* (exceptions are sliced into fixed symbol-count pages, so a point
  lookup only decodes the single covering page).

Explicit dead transitions are represented as a distinguished sentinel that is
kept separate from the default and therefore can never be masked by it.

Images are immutable byte strings. :class:`~paged_dfa.view.View` is a read-only,
demand-paging query interface.  :mod:`paged_dfa.patch` builds a new image from a
small set of transition edits together with a *reversible per-page delta*; the
old view keeps operating on the bytes it already references.
"""

from .errors import (
    IntegrityError,
    InvalidPage,
    MissingPage,
    PageLoadCancelled,
)
from .model import Model, Row, DEAD
from .codec import DFAEncoder
from .view import View, InMemoryBackend
from .patch import Edit, PatchResult, VersionStore

__all__ = [
    "IntegrityError",
    "InvalidPage",
    "MissingPage",
    "PageLoadCancelled",
    "Model",
    "Row",
    "DEAD",
    "DFAEncoder",
    "View",
    "InMemoryBackend",
    "Edit",
    "PatchResult",
    "VersionStore",
]
