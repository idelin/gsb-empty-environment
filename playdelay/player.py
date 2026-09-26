"""Playout engine: adaptive target delay with a monotonic playout position.

Monotonicity policy (explicit):
  * grow   - when the buffered level is below target, synthesize up to
             MAX_GROW_PER_TICK samples per tick (zero-order-hold interpolation
             of the last emitted sample). Synthesized samples are marked in
             `output_flags` and counted in stats.grow_synth_samples.
  * shrink - when the level is above target, skip forward by whole packets
             (plus the remainder of the current packet). Dropped samples are
             counted in stats.dropped_shrink_samples. The playout position
             only ever moves forward, so shrinking can never replay audio.
  * miss   - a source interval not present at its deadline is concealed with
             the same hold interpolation, marked synthesized, and counted as
             missing. A packet arriving afterwards is classified 'late' and
             discarded: already-played intervals are never rewritten.

`output`, `output_flags`, `trajectory`, `monitor` and `real_spans` are
verification recording (test scaffolding) and grow with the run length; the
engine state itself (buffer, estimators, controller) is fixed-memory.
"""
from array import array

from . import config
from .buffer import JitterBuffer
from .clock import ClockEstimator
from .controller import DelayController
from .model import Stats, TickRecord


class PlayoutEngine:
    def __init__(self, nominal_rate=config.SAMPLE_RATE,
                 capacity=config.BUFFER_CAPACITY, record=True):
        self.nominal_rate = nominal_rate
        self.clock = ClockEstimator()
        self.controller = DelayController()
        self.buffer = JitterBuffer(capacity)
        self.record = record
        self.epoch = None
        self.playout_pos = 0
        self.frac = 0.0
        self.started = False
        self.start_time = None
        self.eos_pos = None         # end-of-stream source position, if known
        self.tick_index = 0
        self.pending = []           # delivered packets awaiting the next tick
        self.stats = Stats()
        self.trajectory = []        # list[TickRecord]
        self.monitor = []           # (t, level, target, headroom, slope)
        self.real_spans = []        # (src_pos, length) of real emissions
        self.output = array('f')
        self.output_flags = bytearray()   # 1 -> synthesized sample
        self.last_sample = 0.0
        self._last_missing_idx = -1

    # ---------------- ingress ----------------

    def on_packet(self, pkt, recv_time):
        """Transport hand-off. Packets are processed at the next tick, sorted
        by (recv_time, epoch, seq), so the schedule never depends on the
        arrival *order* of packets sharing a receive time."""
        self.pending.append((recv_time, pkt.epoch, pkt.seq, pkt))

    def _process_pending(self):
        if not self.pending:
            return
        self.pending.sort(key=lambda e: (e[0], e[1], e[2]))
        for recv_time, _epoch, _seq, pkt in self.pending:
            self._accept(pkt, recv_time)
        self.pending.clear()

    def _accept(self, pkt, recv_time):
        s = self.stats
        s.received += 1
        if self.epoch is not None and pkt.epoch < self.epoch:
            s.rejected_old_epoch += 1
            s.old_epoch_samples += len(pkt.payload)
            return
        if self.epoch is None:
            self._reset_epoch(pkt.epoch)
        elif pkt.epoch > self.epoch:
            self.stats.epoch_changes += 1
            self._reset_epoch(pkt.epoch)
        self.clock.update(recv_time, pkt.send_time)
        if self.clock.ready:
            transit = recv_time - self.clock.map_send_to_recv(pkt.send_time)
            self.controller.update(transit)
        outcome = self.buffer.push(pkt, self.playout_pos)
        n = len(pkt.payload)
        if outcome == 'stored':
            s.stored += 1
            if self.start_time is None:
                self.start_time = recv_time + self.controller.target_delay()
        elif outcome == 'duplicate':
            s.duplicates += 1
            s.duplicate_samples += n
        elif outcome == 'late':
            s.late += 1
            s.late_samples += n
        elif outcome == 'overflow':
            s.overflow += 1
            s.overflow_samples += n

    def _reset_epoch(self, epoch):
        self.epoch = epoch
        self.buffer.reset()
        self.clock = ClockEstimator()
        self.controller = DelayController()
        self.playout_pos = 0
        self.frac = 0.0
        self.started = False
        self.start_time = None
        self.eos_pos = None
        self._last_missing_idx = -1

    def note_eos(self, total_samples):
        """End-of-stream notification: no source material beyond this
        position. Playout stops there instead of concealing phantom
        packets."""
        self.eos_pos = total_samples

    # ---------------- playout ----------------

    def tick(self, t, dt=config.TICK):
        self._process_pending()
        if self.start_time is None:
            return
        if not self.started:
            if t + config.TIME_TOL >= self.start_time:
                self.started = True
            else:
                return
        if self.eos_pos is not None and self.playout_pos >= self.eos_pos:
            return
        P = config.PACKET_SAMPLES
        rate = self.clock.playback_rate(self.nominal_rate)
        want = rate * dt + self.frac
        n = int(want)
        self.frac = want - n
        if self.eos_pos is not None:
            n = min(n, self.eos_pos - self.playout_pos)
        level = self.buffer.level(self.playout_pos)
        # keep enough slack for the drop hysteresis + rate limit, otherwise
        # a full buffer would sit exactly at the drop threshold forever
        cap_slack = max(2, self.buffer.capacity - 4) * P
        target = min(self.controller.target_delay() * rate, cap_slack)
        projected = level - n
        n_drop = 0
        n_grow = 0
        if projected - target > config.LEVEL_HYSTERESIS:
            # shrink: skip forward, never backwards
            excess = projected - target
            k = min(config.MAX_DROP_PER_TICK, int(excess // P))
            if k > 0:
                boundary = ((self.playout_pos + P - 1) // P) * P
                skip = (boundary - self.playout_pos) + k * P
                if self.eos_pos is not None:
                    skip = min(skip, self.eos_pos - self.playout_pos)
                self._discard_range(skip)
                n_drop = skip
        elif target - projected > config.LEVEL_HYSTERESIS:
            # grow: insert synthesized samples (marked), position unchanged
            deficit = target - projected
            g = min(config.MAX_GROW_PER_TICK, int(deficit))
            if g > 0:
                self._emit_synth(g, 'grow')
                n_grow = g
        src_start = self.playout_pos
        n_real, n_conceal = self._consume(n)
        self.trajectory.append(
            TickRecord(self.tick_index, t, src_start,
                       n_real, n_conceal + n_grow, n_drop))
        self.monitor.append((t, level, target,
                             self.controller.headroom(), self.clock.slope(),
                             self.controller.jit))
        self.tick_index += 1

    def _consume(self, n):
        real = 0
        synth = 0
        remaining = n
        P = config.PACKET_SAMPLES
        while remaining > 0:
            pkt = self.buffer.packet_at(self.playout_pos)
            if pkt is not None:
                off = self.playout_pos - pkt.src_pos
                take = min(remaining, len(pkt.payload) - off)
                self._emit_real(pkt.payload[off:off + take])
                if self.record:
                    self.real_spans.append((self.playout_pos, take))
                real += take
                self.playout_pos += take
                remaining -= take
                if off + take == len(pkt.payload):
                    del self.buffer.packets[pkt.seq]
            else:
                nxt = self.buffer.next_start_after(self.playout_pos)
                boundary = (self.playout_pos // P + 1) * P
                stop = min(boundary, nxt) if nxt is not None else boundary
                take = min(remaining, stop - self.playout_pos)
                idx = self.playout_pos // P
                if idx != self._last_missing_idx:
                    self.stats.missing_packets += 1
                    self._last_missing_idx = idx
                self._emit_synth(take, 'conceal')
                synth += take
                self.playout_pos += take
                remaining -= take
        return real, synth

    def _discard_range(self, skip):
        """Advance the playout position by `skip` samples (shrink policy).
        Buffered samples in the range are counted as dropped; source
        positions not present in the buffer are counted as skipped-missing
        (they were never received, so they cannot be 'dropped')."""
        P = config.PACKET_SAMPLES
        end = self.playout_pos + skip
        pos = self.playout_pos
        while pos < end:
            pkt = self.buffer.packet_at(pos)
            if pkt is not None:
                take = min(end, pkt.src_pos + len(pkt.payload)) - pos
                self.stats.dropped_shrink_samples += take
            else:
                nxt = self.buffer.next_start_after(pos)
                boundary = (pos // P + 1) * P
                stop = min(boundary, nxt) if nxt is not None else boundary
                take = min(end, stop) - pos
                idx = pos // P
                if idx != self._last_missing_idx:
                    self.stats.missing_packets += 1
                    self._last_missing_idx = idx
                self.stats.skipped_missing_samples += take
            pos += take
        self.playout_pos = end
        self.buffer.discard_before(self.playout_pos)

    # ---------------- emission ----------------

    def _emit_real(self, samples):
        self.stats.real_samples += len(samples)
        if self.record:
            self.output.extend(samples)
            self.output_flags.extend(b'\x00' * len(samples))
        self.last_sample = samples[-1]

    def _emit_synth(self, count, kind):
        """Zero-order-hold interpolation; samples are marked synthesized."""
        if kind == 'grow':
            self.stats.grow_synth_samples += count
        else:
            self.stats.missing_samples += count
        if self.record:
            self.output.extend([self.last_sample] * count)
            self.output_flags.extend(b'\x01' * count)
