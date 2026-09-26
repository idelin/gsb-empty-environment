"""Change events carrying a global sequence number (seq)."""
from __future__ import annotations

from dataclasses import dataclass

OP_UPSERT = "upsert"
OP_DELETE = "delete"
_OPS = (OP_UPSERT, OP_DELETE)


@dataclass(frozen=True)
class Event:
    """A single document change.

    seq:      global monotonic sequence number, starts at 1, no gaps allowed
              (gaps are detected and reported, never silently skipped).
    doc_id:   document identifier.
    op:       "upsert" (create/replace full text) or "delete" (tombstone).
    text:     UTF-8 text for upsert; must be None for delete.
    version:  optional declared version for conflict resolution. When None,
              the seq is used, which makes the stream order authoritative.
              A event is applied to a document only when its effective
              version is strictly greater than the document's current one.
    """

    seq: int
    doc_id: str
    op: str
    text: str | None = None
    version: int | None = None

    def __post_init__(self) -> None:
        if not isinstance(self.seq, int) or self.seq < 1:
            raise ValueError(f"seq must be an int >= 1, got {self.seq!r}")
        if not self.doc_id:
            raise ValueError("doc_id must be a non-empty string")
        if self.op not in _OPS:
            raise ValueError(f"op must be one of {_OPS}, got {self.op!r}")
        if self.op == OP_UPSERT and self.text is None:
            raise ValueError("upsert requires text")
        if self.op == OP_DELETE and self.text is not None:
            raise ValueError("delete must not carry text")

    @property
    def effective_version(self) -> int:
        return self.version if self.version is not None else self.seq

    def to_dict(self) -> dict:
        return {
            "seq": self.seq,
            "doc_id": self.doc_id,
            "op": self.op,
            "text": self.text,
            "version": self.version,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "Event":
        return cls(
            seq=data["seq"],
            doc_id=data["doc_id"],
            op=data["op"],
            text=data.get("text"),
            version=data.get("version"),
        )
