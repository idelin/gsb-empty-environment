"""Receiver: reordering jitter buffer + monotonic playout renderer.

Policies (all explicit, all traceable via self.stats):
  - old epoch packets are rejected (old_epoch_*)
  - packets whose source position is already played are rejected as
    late; the played interval is never rewritten (late_*)
  - duplicates of buffered packets are dropped (duplicate_*)
  - a full buffer keeps the `capacity` lowest sequence numbers; the
    rest are dropped (overflow_*)
  - delay shrink consumes faster and DROPS real samples (dropped_samples)
  - delay grow consumes slower and SYNTHESIZES samples (grow_synth)
  - missing data is concealed with synthesized samples (conceal_missing)
  - synthesized samples are always marked in the trace and counted in
    emitted_synth; play_pos is strictly increasing, so nothing is ever
    replayed.

Memory is fixed: the buffer holds at most `capacity` packets, the
estimator/controller state is O(1). The trace is test instrumentation
and only collected when trace_on=True.
"""

STATS_KEYS = (
    "accepted_packets", "accepted_samples",
    "duplicate_packets", "duplicate_samples",
    "overflow_packets", "overflow_samples",
    "late_packets", "late_samples",
    "old_epoch_packets", "old_epoch_samples",
    "malformed_packets",
    "epoch_resets", "epoch_flushed_samples",
    "emitted_true", "emitted_synth",
    "conceal_missing_samples", "grow_synth_samples",
    "preroll_synth_samples", "dropped_samples",
)


class Receiver:
    def __init__(self, fs, packet_len, block, capacity, estimator,
                 controller, trace_on=False, start_timeout_ticks=60):
        self.fs = fs
        self.packet_len = packet_len
        self.block = block
        self.capacity = capacity
        self.estimator = estimator
        self.controller = controller
        self.trace_on = trace_on
        self.start_timeout_ticks = start_timeout_ticks
        self.epoch = None
        self.buffer = {}            # seq -> Packet, current epoch only
        self.play_pos = 0.0         # source position of next output sample
        self.started = False
        self.waiting = 0
        self.tick_index = 0
        self.pending_residuals = []  # (recv_time, offset) since last tick
        self.stats = {k: 0 for k in STATS_KEYS}
        self.trace = []
        self.last_advance = 0.0
        self.last_level = 0.0

    def on_packet(self, pkt):
        if pkt.src_pos != pkt.seq * self.packet_len:
            self.stats["malformed_packets"] += 1
            return
        if self.epoch is None or pkt.epoch > self.epoch:
            self._new_epoch(pkt.epoch)
        elif pkt.epoch < self.epoch:
            self.stats["old_epoch_packets"] += 1
            self.stats["old_epoch_samples"] += len(pkt.samples)
            return
        if pkt.src_pos < int(self.play_pos):
            # Already (partly) played: reject, never rewrite history.
            self.stats["late_packets"] += 1
            self.stats["late_samples"] += len(pkt.samples)
            return
        offset = pkt.recv_time - pkt.src_pos / self.fs
        self.estimator.add(pkt.recv_time, offset)
        self.pending_residuals.append((pkt.recv_time, offset))
        n = len(pkt.samples)
        if pkt.seq in self.buffer:
            self.stats["duplicate_packets"] += 1
            self.stats["duplicate_samples"] += n
            return
        if len(self.buffer) >= self.capacity:
            max_seq = max(self.buffer)
            if pkt.seq > max_seq:
                self.stats["overflow_packets"] += 1
                self.stats["overflow_samples"] += n
                return
            evicted = self.buffer.pop(max_seq)
            # The evicted packet was accepted earlier; move its samples
            # from the accepted account to the overflow account so the
            # conservation identity keeps balancing.
            self.stats["accepted_packets"] -= 1
            self.stats["accepted_samples"] -= len(evicted.samples)
            self.stats["overflow_packets"] += 1
            self.stats["overflow_samples"] += len(evicted.samples)
        self.buffer[pkt.seq] = pkt
        self.stats["accepted_packets"] += 1
        self.stats["accepted_samples"] += n

    def _new_epoch(self, epoch):
        if self.buffer:
            self.stats["epoch_flushed_samples"] += sum(
                len(p.samples) for p in self.buffer.values())
        self.buffer = {}
        if self.epoch is not None:
            self.stats["epoch_resets"] += 1
        self.epoch = epoch
        self.started = False
        self.play_pos = 0.0
        self.waiting = 0
        self.pending_residuals = []
        self.estimator.reset_bins()

    def on_tick(self, now):
        self.estimator.ensure_fit()
        if self.pending_residuals:
            candidate = 0.0
            if self.estimator.have_fit:
                for recv_time, offset in self.pending_residuals:
                    r = self.estimator.residual(recv_time, offset)
                    if r > candidate:
                        candidate = r
            self.controller.observe(candidate)
            self.pending_residuals = []
        self.controller.tick()
        true = synth = dropped = 0
        advance = 0.0
        level = 0.0
        if not self.started:
            self._try_start()
        if not self.started:
            synth = self.block
            self.stats["preroll_synth_samples"] += synth
            self.stats["emitted_synth"] += synth
        else:
            level = self._level()
            correction = self.controller.correction_samples(level, self.block)
            advance = self.block * self.estimator.rate_ratio + correction
            advance = min(max(advance, 0.25 * self.block), 3.0 * self.block)
            true, synth, dropped = self._consume(advance)
            self._evict_consumed()
        self.last_advance = advance
        self.last_level = level
        if self.trace_on:
            self.trace.append([
                self.tick_index, now, 1 if self.started else 0,
                self.play_pos, advance, true, synth, dropped, level,
                self.controller.target_delay_s * self.fs,
                self.estimator.drift_ppm,
            ])
        self.tick_index += 1

    def _try_start(self):
        if not self.buffer:
            self.waiting += 1
            return
        s0 = min(self.buffer)
        seq = s0
        contig_end = None
        while seq in self.buffer:
            pkt = self.buffer[seq]
            contig_end = pkt.src_pos + self.packet_len
            seq += 1
        start_pos = self.buffer[s0].src_pos
        enough = contig_end - start_pos >= \
            self.controller.target_delay_s * self.fs
        if enough or self.waiting > self.start_timeout_ticks:
            self.play_pos = float(start_pos)
            self.started = True
        else:
            self.waiting += 1

    def _level(self):
        if not self.buffer:
            return 0.0
        newest = max(p.src_pos + self.packet_len
                     for p in self.buffer.values())
        return max(0.0, newest - self.play_pos)

    def _has(self, index):
        seq, _off = divmod(index, self.packet_len)
        pkt = self.buffer.get(seq)
        return pkt is not None and pkt.src_pos == seq * self.packet_len

    def _consume(self, advance):
        """Advance play_pos by `advance` source samples and emit one
        block. Consumed regions tile contiguously because play_pos is
        continuous and strictly increasing."""
        p0 = self.play_pos
        p1 = p0 + advance
        i0 = int(p0)
        i1 = int(p1)
        if i1 <= i0:
            i1 = i0 + 1
        span = i1 - i0
        emit = span if span < self.block else self.block
        true = missing = 0
        for idx in range(i0, i0 + emit):
            if self._has(idx):
                true += 1
            else:
                missing += 1
        dropped = 0
        for idx in range(i0 + emit, i1):
            if self._has(idx):
                dropped += 1
        grow = self.block - emit if emit < self.block else 0
        synth = missing + grow
        self.stats["emitted_true"] += true
        self.stats["emitted_synth"] += synth
        self.stats["conceal_missing_samples"] += missing
        self.stats["grow_synth_samples"] += grow
        self.stats["dropped_samples"] += dropped
        self.play_pos = p1
        return true, synth, dropped

    def _evict_consumed(self):
        cut = int(self.play_pos)
        for seq in [s for s, p in self.buffer.items()
                    if p.src_pos + self.packet_len <= cut]:
            del self.buffer[seq]

    def to_state(self):
        return {
            "epoch": self.epoch,
            "buffer": {str(s): p.to_list() for s, p in self.buffer.items()},
            "play_pos": self.play_pos,
            "started": self.started,
            "waiting": self.waiting,
            "tick_index": self.tick_index,
            "pending_residuals": [list(r) for r in self.pending_residuals],
            "stats": dict(self.stats),
            "trace": [list(r) for r in self.trace],
            "last_advance": self.last_advance,
            "last_level": self.last_level,
        }

    def load_state(self, state):
        from .packets import Packet
        self.epoch = state["epoch"]
        self.buffer = {int(s): Packet.from_list(p)
                       for s, p in state["buffer"].items()}
        self.play_pos = state["play_pos"]
        self.started = state["started"]
        self.waiting = state["waiting"]
        self.tick_index = state["tick_index"]
        self.pending_residuals = [tuple(r) for r in state["pending_residuals"]]
        self.stats = {k: int(v) for k, v in state["stats"].items()}
        self.trace = [list(r) for r in state["trace"]]
        self.last_advance = state["last_advance"]
        self.last_level = state["last_level"]
