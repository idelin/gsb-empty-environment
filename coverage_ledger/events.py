"""Append-only ledger events."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Union

from .models import Membership, Report


@dataclass(frozen=True)
class AddReport:
    report: Report


@dataclass(frozen=True)
class RetractReport:
    report_id: str


@dataclass(frozen=True)
class AddMembership:
    membership: Membership


@dataclass(frozen=True)
class RetractMembership:
    membership_id: str


Event = Union[AddReport, RetractReport, AddMembership, RetractMembership]


@dataclass(frozen=True)
class RevisionEntry:
    revision: int
    events: tuple[Event, ...]
    reason: str = ""
