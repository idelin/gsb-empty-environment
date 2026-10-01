"""Audit-trail events emitted by the solver and replayed by the checker.

Event kinds:
  decide     {var, value}            branch: reduce domain of var to {value}
  propagate  {var, value, constraint} delete `value` from domain of var,
                                      justified by constraint index (no support)
  conflict   {var}                   domain of var became empty
  backtrack  {level, domains}        restore snapshot; `domains` is the full
                                      restored domain map, for verification
  exhausted  {var}                   every value of var at this level failed
  solution   {assignment}            terminal: complete assignment claimed
  unsat      {}                      terminal: exhaustive search failed
  unknown    {reason}                terminal: search inconclusive (budget)
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Event:
    kind: str
    seq: int
    payload: dict

    def to_dict(self) -> dict:
        return {"seq": self.seq, "kind": self.kind, "payload": self.payload}

    @staticmethod
    def from_dict(d: dict) -> "Event":
        return Event(kind=d["kind"], seq=d["seq"], payload=d["payload"])

    def __str__(self) -> str:
        return f"#{self.seq:03d} {self.kind}: {self.payload}"
