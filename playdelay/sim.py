"""Discrete-event simulation of source, network and receiver.

Sim time is the receiver clock. Events are plain data tuples
(time, priority, order, kind, payload) so a whole Runner (event queue, RNG
state, player state) can be pickled mid-run and restored bit-exactly.

Network models:
  * continuous: each packet gets transit = base +/- jitter (bursts can raise
    the jitter over chosen intervals), plus optional loss and duplication;
  * batched: deterministic per-packet transit pattern, deliveries quantized to
    batch boundaries and shuffled within a batch by the seed. Receive times
    are seed-independent; only the delivery *order* is random.
"""
import heapq
import math
import pickle
import random
from dataclasses import dataclass

from . import config
from .fixtures import source_block
from .model import Packet
from .player import PlayoutEngine

# event priorities at equal timestamps
PRIO_EMIT = 0
PRIO_DELIVER = 1
PRIO_EOS = 1
PRIO_TICK = 2


class EventSim:
    def __init__(self):
        self.now = 0.0
        self._q = []
        self._order = 0

    def schedule(self, t, prio, kind, payload=None):
        heapq.heappush(self._q, (t, prio, self._order, kind, payload))
        self._order += 1

    def peek_time(self):
        return self._q[0][0] if self._q else None

    def pop(self):
        t, _prio, _order, kind, payload = heapq.heappop(self._q)
        self.now = t
        return kind, payload

    def __len__(self):
        return len(self._q)


@dataclass
class Scenario:
    duration: float = 10.0          # seconds of source material
    drift: float = 0.0              # source-clock drift (relative, e.g. 5e-3)
    base_transit: float = 0.03      # s
    jitter: float = 0.01            # s, uniform +/- bound
    loss: float = 0.0               # packet loss probability
    dup: float = 0.0                # packet duplication probability
    bursts: tuple = ()              # (t0, t1, jitter) jitter bursts
    stall: tuple = ()               # (t0, t1) deliveries held until t1
    batched: bool = False           # quantized, order-shuffled deliveries
    batch_period: float = 0.1
    transit_pattern: tuple = (0.0, 0.03, 0.06)
    capacity: int = config.BUFFER_CAPACITY
    tail: float = 1.0               # extra playout time after the last packet


class Network:
    """Copy-level accounting: generated + dup_created == lost + delivered."""

    def __init__(self, scenario, seed):
        self.sc = scenario
        self.rng = random.Random(seed)
        self.generated = 0
        self.lost = 0
        self.dup_created = 0
        self.delivered = 0

    def transit(self, t):
        sc = self.sc
        j = sc.jitter
        for (t0, t1, bj) in sc.bursts:
            if t0 <= t < t1:
                j = bj
        d = sc.base_transit + self.rng.uniform(-j, j)
        if sc.stall and sc.stall[0] <= t < sc.stall[1]:
            d += sc.stall[1] - t      # held back, released in a clump
        return max(d, 1e-4)


class Runner:
    def __init__(self, scenario, seed=1):
        self.sc = scenario
        self.sim = EventSim()
        self.net = Network(scenario, seed)
        self.player = PlayoutEngine(capacity=scenario.capacity)
        self.arrival_log = []       # sequence numbers in delivery order
        n_packets = int(scenario.duration / config.PACKET_PERIOD)
        if scenario.batched:
            self._schedule_batched(n_packets)
        else:
            for i in range(n_packets):
                self.sim.schedule(i * config.PACKET_PERIOD, PRIO_EMIT, 'emit', i)
        self.sim.schedule(scenario.duration, PRIO_EOS, 'eos',
                          n_packets * config.PACKET_SAMPLES)
        k = 0
        end = scenario.duration + scenario.tail
        while k * config.TICK <= end + config.TIME_TOL:
            self.sim.schedule(k * config.TICK, PRIO_TICK, 'tick')
            k += 1

    # ---- source ----

    def _make_packet(self, i):
        t = i * config.PACKET_PERIOD
        send_ts = (1.0 + self.sc.drift) * t
        payload = tuple(source_block(i * config.PACKET_SAMPLES,
                                     config.PACKET_SAMPLES))
        return Packet(0, i, i * config.PACKET_SAMPLES, send_ts, payload)

    # ---- network ----

    def _schedule_batched(self, n_packets):
        sc = self.sc
        batches = {}
        for i in range(n_packets):
            t = i * config.PACKET_PERIOD
            transit = sc.base_transit + sc.transit_pattern[i % len(sc.transit_pattern)]
            raw = t + transit
            batch = math.ceil(raw / sc.batch_period - 1e-9) * sc.batch_period
            batches.setdefault(batch, []).append(i)
        for batch in sorted(batches):
            ids = batches[batch]
            self.net.rng.shuffle(ids)     # random delivery order
            for i in ids:
                self.sim.schedule(batch, PRIO_DELIVER, 'deliver',
                                  self._make_packet(i))
                self.net.generated += 1

    def on_emit(self, i):
        pkt = self._make_packet(i)
        self.net.generated += 1
        if self.net.rng.random() < self.sc.loss:
            self.net.lost += 1
            return
        t = i * config.PACKET_PERIOD
        transit = self.net.transit(t)
        self.sim.schedule(t + transit, PRIO_DELIVER, 'deliver', pkt)
        if self.net.rng.random() < self.sc.dup:
            self.net.dup_created += 1
            self.sim.schedule(t + transit + 0.005, PRIO_DELIVER, 'deliver', pkt)

    # ---- receiver ----

    def on_deliver(self, pkt):
        self.net.delivered += 1
        self.arrival_log.append(pkt.seq)
        self.player.on_packet(pkt, self.sim.now)

    # ---- driving ----

    def step(self):
        kind, payload = self.sim.pop()
        if kind == 'emit':
            self.on_emit(payload)
        elif kind == 'deliver':
            self.on_deliver(payload)
        elif kind == 'eos':
            self.player.note_eos(payload)
        else:
            self.player.tick(self.sim.now)

    def run(self):
        while len(self.sim):
            self.step()

    def run_until(self, t):
        while len(self.sim) and self.sim.peek_time() <= t + config.TIME_TOL:
            self.step()

    # ---- persistence ----

    def snapshot(self):
        return pickle.dumps(self)

    @staticmethod
    def restore(data):
        return pickle.loads(data)
