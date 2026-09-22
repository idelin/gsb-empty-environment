from __future__ import annotations

import heapq
from collections import Counter
from dataclasses import dataclass, field


@dataclass
class SourceState:
    source_id: str
    epoch: str
    floor: int
    watermark: int
    active: bool = True
    rx_prefix: int = 0
    ack_prefix: int = 0
    highest_rx_seq: int = 0
    highest_claim_cut: int = 0
    pending_claim_cut: int = 0
    pending_claim_counts: Counter[int] = field(default_factory=Counter)
    pending_received: dict[int, int] = field(default_factory=dict)
    unacked: dict[int, int] = field(default_factory=dict)
    claims_heap: list[tuple[int, int]] = field(default_factory=list)
    version: int = 0

    @property
    def blocked(self) -> bool:
        self.drain_claims()
        return bool(self.unacked) or self.pending_claim_cut > self.rx_prefix

    def receive_data(self, seq: int, event_time: int) -> str:
        if seq <= self.rx_prefix or seq in self.pending_received:
            return "duplicate"
        self.pending_received[seq] = event_time
        self.unacked[seq] = event_time
        self.highest_rx_seq = max(self.highest_rx_seq, seq)
        next_seq = self.rx_prefix + 1
        while next_seq in self.pending_received:
            self.pending_received.pop(next_seq)
            self.rx_prefix = next_seq
            next_seq += 1
        self._advance_ack_prefix()
        self.version += 1
        return "applied"

    def ack(self, seq: int) -> str:
        if seq <= self.ack_prefix:
            return "duplicate"
        if seq not in self.unacked:
            return "unknown"
        self.unacked.pop(seq)
        self._advance_ack_prefix()
        self.version += 1
        return "applied"

    def _advance_ack_prefix(self) -> None:
        while self.ack_prefix < self.rx_prefix and self.ack_prefix + 1 not in self.unacked:
            self.ack_prefix += 1
        self.drain_claims()

    def add_progress(self, cut_seq: int, frontier: int) -> None:
        heapq.heappush(self.claims_heap, (cut_seq, frontier))
        self.pending_claim_counts[cut_seq] += 1
        self.highest_claim_cut = max(self.highest_claim_cut, cut_seq)
        if cut_seq > self.ack_prefix:
            self.pending_claim_cut = max(self.pending_claim_cut, cut_seq)
        self.drain_claims()
        self.version += 1

    def drain_claims(self) -> None:
        changed = False
        while self.claims_heap and self.claims_heap[0][0] <= self.ack_prefix:
            cut, frontier = heapq.heappop(self.claims_heap)
            self.pending_claim_counts[cut] -= 1
            if self.pending_claim_counts[cut] == 0:
                del self.pending_claim_counts[cut]
            if frontier > self.watermark:
                self.watermark = frontier
                changed = True
        self.pending_claim_cut = max(self.pending_claim_counts, default=0)
        if changed:
            self.version += 1

    def to_dict(self) -> dict:
        return {
            "source_id": self.source_id,
            "epoch": self.epoch,
            "floor": self.floor,
            "watermark": self.watermark,
            "active": self.active,
            "rx_prefix": self.rx_prefix,
            "ack_prefix": self.ack_prefix,
            "highest_rx_seq": self.highest_rx_seq,
            "highest_claim_cut": self.highest_claim_cut,
            "pending_claim_cut": self.pending_claim_cut,
            "pending_claim_counts": dict(self.pending_claim_counts),
            "pending": [[seq, event_time] for seq, event_time in self.pending_received.items()],
            "unacked": [[seq, event_time] for seq, event_time in self.unacked.items()],
            "claims_heap": [list(item) for item in self.claims_heap],
            "version": self.version,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "SourceState":
        pending = data.get("pending_received", data.get("pending", []))
        state = cls(
            source_id=data["source_id"],
            epoch=data["epoch"],
            floor=data["floor"],
            watermark=data["watermark"],
            active=data["active"],
            rx_prefix=data["rx_prefix"],
            ack_prefix=data["ack_prefix"],
            highest_rx_seq=data["highest_rx_seq"],
            highest_claim_cut=data["highest_claim_cut"],
            pending_claim_cut=data.get("pending_claim_cut", 0),
            pending_claim_counts=Counter({int(key): value for key, value in data.get("pending_claim_counts", {}).items()}),
            pending_received={int(seq): event_time for seq, event_time in pending},
            unacked={int(seq): event_time for seq, event_time in data["unacked"]},
            claims_heap=[(int(cut), int(frontier)) for cut, frontier in data["claims_heap"]],
            version=data["version"],
        )
        heapq.heapify(state.claims_heap)
        if not state.pending_claim_counts and state.claims_heap:
            state.pending_claim_counts = Counter(cut for cut, _ in state.claims_heap)
            state.pending_claim_cut = max(state.pending_claim_counts, default=0)
        return state


@dataclass
class ChildParticipant:
    source_id: str
    epoch: str
    confirmed: bool = False
    state: SourceState | None = None

    def to_dict(self) -> dict:
        return {
            "source_id": self.source_id,
            "epoch": self.epoch,
            "confirmed": self.confirmed,
            "state": self.state.to_dict() if self.state is not None else None,
        }

    @classmethod
    def from_dict(cls, data: dict) -> "ChildParticipant":
        return cls(
            source_id=data["source_id"],
            epoch=data["epoch"],
            confirmed=data["confirmed"],
            state=SourceState.from_dict(data["state"]) if data.get("state") else None,
        )


@dataclass
class HandoverState:
    handover_id: str
    parent_id: str
    parent_epoch: str
    boundary_h: int
    terminal_cut: int
    terminal_frontier: int | None = None
    status: str = "pending"
    children: dict[str, ChildParticipant] = field(default_factory=dict)

    def to_dict(self) -> dict:
        return {
            "handover_id": self.handover_id,
            "parent_id": self.parent_id,
            "parent_epoch": self.parent_epoch,
            "boundary_h": self.boundary_h,
            "terminal_cut": self.terminal_cut,
            "terminal_frontier": self.terminal_frontier,
            "status": self.status,
            "children": [child.to_dict() for child in self.children.values()],
        }

    @classmethod
    def from_dict(cls, data: dict) -> "HandoverState":
        return cls(
            handover_id=data["handover_id"],
            parent_id=data["parent_id"],
            parent_epoch=data["parent_epoch"],
            boundary_h=data["boundary_h"],
            terminal_cut=data["terminal_cut"],
            terminal_frontier=data.get("terminal_frontier"),
            status=data["status"],
            children={
                child["source_id"]: ChildParticipant.from_dict(child)
                for child in data["children"]
            },
        )
