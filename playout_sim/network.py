"""Network model: base delay + uniform jitter + deterministic burst
windows, optional loss/duplication, optional batched (shuffled)
delivery. All randomness comes from seeded RNGs whose state is part of
the snapshot."""

import random

from .packets import Packet


def _rng_to_state(rng):
    version, internal, gauss = rng.getstate()
    return [version, list(internal), gauss]


def _rng_from_state(state):
    rng = random.Random()
    rng.setstate((state[0], tuple(state[1]), state[2]))
    return rng


class Network:
    def __init__(self, sim, base_delay_s, normal_jitter, burst_jitter,
                 burst_windows, loss_prob, dup_prob, loss_seqs,
                 seed, shuffle_seed, batch):
        self.sim = sim
        self.base_delay_s = base_delay_s
        self.normal_jitter = tuple(normal_jitter)
        self.burst_jitter = tuple(burst_jitter)
        self.burst_windows = [tuple(w) for w in burst_windows]
        self.loss_prob = loss_prob
        self.dup_prob = dup_prob
        self.loss_seqs = {tuple(k) for k in loss_seqs}
        self.rng = random.Random(seed)
        self.shuffle_rng = random.Random(shuffle_seed)
        self.batch = batch
        self.pending = []           # batch mode: arrived, not delivered
        self.lost_packets = 0
        self.lost_samples = 0
        self.dup_injected_packets = 0
        self.dup_injected_samples = 0
        self.receiver = None        # wired by World

    def _extra_delay(self, now):
        lo, hi = self.normal_jitter
        extra = self.rng.uniform(lo, hi)
        for t0, t1 in self.burst_windows:
            if t0 <= now < t1:
                blo, bhi = self.burst_jitter
                extra += self.rng.uniform(blo, bhi)
                break
        return extra

    def send(self, pkt, now):
        key = (pkt.epoch, pkt.seq)
        if key in self.loss_seqs or self.rng.random() < self.loss_prob:
            self.lost_packets += 1
            self.lost_samples += len(pkt.samples)
            return
        recv = now + self.base_delay_s + self._extra_delay(now)
        self._deliver(pkt, recv)
        if self.rng.random() < self.dup_prob:
            self.dup_injected_packets += 1
            self.dup_injected_samples += len(pkt.samples)
            self._deliver(pkt, recv + 0.001)

    def _deliver(self, pkt, recv):
        stamped = Packet(pkt.epoch, pkt.seq, pkt.src_pos, recv, pkt.samples)
        if self.batch:
            self.pending.append(stamped)
        else:
            self.sim.schedule_at(recv, "arrive", stamped.to_list(), prio=0)

    def flush(self, now):
        """Deliver everything whose receive time has passed, in shuffled
        (random) order. The receiver is order-independent within a tick
        window, so this cannot change the playout schedule."""
        if not self.pending:
            return
        ready = [p for p in self.pending if p.recv_time <= now + 1e-12]
        if not ready:
            return
        self.pending = [p for p in self.pending
                        if p.recv_time > now + 1e-12]
        self.shuffle_rng.shuffle(ready)
        for pkt in ready:
            self.receiver.on_packet(pkt)

    def to_state(self):
        return {
            "pending": [p.to_list() for p in self.pending],
            "lost_packets": self.lost_packets,
            "lost_samples": self.lost_samples,
            "dup_injected_packets": self.dup_injected_packets,
            "dup_injected_samples": self.dup_injected_samples,
            "rng": _rng_to_state(self.rng),
            "shuffle_rng": _rng_to_state(self.shuffle_rng),
        }

    def load_state(self, state):
        self.pending = [Packet.from_list(p) for p in state["pending"]]
        self.lost_packets = state["lost_packets"]
        self.lost_samples = state["lost_samples"]
        self.dup_injected_packets = state["dup_injected_packets"]
        self.dup_injected_samples = state["dup_injected_samples"]
        self.rng = _rng_from_state(state["rng"])
        self.shuffle_rng = _rng_from_state(state["shuffle_rng"])
