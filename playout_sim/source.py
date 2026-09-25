"""Packet source driven by the event sim. The source clock drifts
relative to the sim (local) clock by a declared ppm value."""

from .fixtures import packet_samples
from .packets import Packet


class Source:
    def __init__(self, fs, packet_len, drift_ppm):
        self.fs = fs
        self.packet_len = packet_len
        self.drift_ppm = drift_ppm
        self.epoch = 0
        self.seq = 0
        self.generation = 0        # invalidates stale send events
        self.next_send_time = 0.0
        self.sent_packets = 0
        self.sent_samples = 0

    @property
    def period_local(self):
        """Send period measured on the local (sim) clock."""
        return self.packet_len / (self.fs * (1.0 + self.drift_ppm * 1e-6))

    def make_packet(self, now):
        src_pos = self.seq * self.packet_len
        pkt = Packet(self.epoch, self.seq, src_pos, 0.0,
                     packet_samples(src_pos, self.packet_len))
        self.seq += 1
        self.sent_packets += 1
        self.sent_samples += self.packet_len
        self.next_send_time = now + self.period_local
        return pkt

    def restart(self, now):
        """Stream restart: new epoch, sequence and src_pos restart at 0."""
        self.epoch += 1
        self.seq = 0
        self.generation += 1
        self.next_send_time = now

    def to_state(self):
        return {
            "epoch": self.epoch,
            "seq": self.seq,
            "generation": self.generation,
            "next_send_time": self.next_send_time,
            "sent_packets": self.sent_packets,
            "sent_samples": self.sent_samples,
        }

    def load_state(self, state):
        self.epoch = state["epoch"]
        self.seq = state["seq"]
        self.generation = state["generation"]
        self.next_send_time = state["next_send_time"]
        self.sent_packets = state["sent_packets"]
        self.sent_samples = state["sent_samples"]
