"""Stream packet: epoch, sequence number, source sample position,
receive time and the sample payload. JSON-friendly for snapshots."""

from dataclasses import dataclass


@dataclass(frozen=True)
class Packet:
    epoch: int          # stream epoch; a restart bumps the epoch
    seq: int            # sequence number within the epoch
    src_pos: int        # source sample index of the first sample
    recv_time: float    # local receive clock, stamped by the network
    samples: tuple      # payload samples from the fixture

    def to_list(self):
        return [self.epoch, self.seq, self.src_pos, self.recv_time,
                list(self.samples)]

    @staticmethod
    def from_list(parts):
        return Packet(int(parts[0]), int(parts[1]), int(parts[2]),
                      float(parts[3]), tuple(parts[4]))
