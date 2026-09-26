"""Shared data model.

A stream packet carries: stream epoch, sequence number, source sample position
of the first payload sample, and the source-side send timestamp. The receive
time is supplied by the transport alongside the packet (see Runner.on_deliver
and PlayoutEngine.on_packet).
"""
from dataclasses import dataclass
from typing import NamedTuple


@dataclass(frozen=True)
class Packet:
    epoch: int          # stream epoch
    seq: int            # sequence number within the epoch
    src_pos: int        # source sample position of payload[0]
    send_time: float    # source-clock time when the packet was sent
    payload: tuple      # samples (floats)


class TickRecord(NamedTuple):
    """One entry of the playout schedule trajectory (all integers / exact
    tick times, so trajectories are comparable with == across runs)."""
    tick: int           # playout tick index
    t: float            # receiver time of the tick
    src_pos: int        # source position at the start of the tick
    n_real: int         # real samples emitted this tick
    n_synth: int        # synthesized samples emitted this tick (grow + conceal)
    n_drop: int         # samples dropped this tick by the shrink policy


@dataclass
class Stats:
    """Traceability counters. Every received copy is classified exactly once
    (stored / duplicate / late / overflow / old-epoch); every generated source
    sample ends up played, concealed or dropped."""
    received: int = 0
    stored: int = 0
    duplicates: int = 0
    late: int = 0
    overflow: int = 0
    rejected_old_epoch: int = 0
    missing_packets: int = 0
    epoch_changes: int = 0
    real_samples: int = 0
    missing_samples: int = 0        # concealed (synthesized) at the deadline
    grow_synth_samples: int = 0     # synthesized to grow the target delay
    dropped_shrink_samples: int = 0     # buffered samples discarded by shrink
    skipped_missing_samples: int = 0    # absent samples jumped over by shrink
    late_samples: int = 0
    overflow_samples: int = 0
    duplicate_samples: int = 0
    old_epoch_samples: int = 0

    @property
    def synth_samples(self):
        return self.missing_samples + self.grow_synth_samples
