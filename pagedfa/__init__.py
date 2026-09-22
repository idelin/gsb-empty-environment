"""Paged, compressed transition tables for large DFAs."""

from .dfa import Alphabet, DFA, language_equivalent
from .encoder import Encoder, EncoderReport
from .store import CallbackProvider, TableView, TableWriter
from .patching import CellEdit, DefaultEdit, PageDelta, apply_delta, inverse_delta
from .errors import (
    BadChecksumError,
    BadTargetError,
    CorruptOffsetError,
    IndexError as InvalidIndexError,
    LoadCancelledError,
    MissingPageError,
    PatchError,
)

__all__ = [
    "Alphabet",
    "DFA",
    "language_equivalent",
    "Encoder",
    "EncoderReport",
    "CallbackProvider",
    "TableView",
    "TableWriter",
    "CellEdit",
    "DefaultEdit",
    "PageDelta",
    "apply_delta",
    "inverse_delta",
    "BadChecksumError",
    "BadTargetError",
    "CorruptOffsetError",
    "InvalidIndexError",
    "LoadCancelledError",
    "MissingPageError",
    "PatchError",
]
