"""World: wires sim + source + network + receiver together.

Event kinds (all payloads are JSON-friendly data):
  send        source emits a packet (payload: source generation)
  arrive      a packet is delivered to the receiver (payload: packet)
  tick        render tick: flush batch network, render one block
  restart     source restarts the stream (new epoch)
  set_margin  change the controller base margin (payload: seconds)

The entire world state can be snapshotted to a JSON-safe dict and
restored, continuing bit-identically.
"""

from .clockest import DriftEstimator
from .controller import DelayController
from .events import EventSim
from .network import Network
from .packets import Packet
from .receiver import Receiver
from .source import Source

DEFAULTS = {
    "fs": 48000,
    "packet_len": 480,
    "block": 480,
    "capacity": 24,               # jitter buffer size in packets (fixed)
    "base_margin_s": 0.025,       # declared base latency
    "init_headroom_s": 0.015,
    "drift_ppm": 0.0,             # source clock drift vs local clock
    "duration": 5.0,              # source stops sending after this
    "base_delay_s": 0.030,
    "normal_jitter": (0.0, 0.002),
    "burst_jitter": (0.02, 0.07),
    "burst_windows": (),
    "loss_prob": 0.0,
    "dup_prob": 0.0,
    "loss_seqs": (),              # deterministic (epoch, seq) losses
    "seed": 1,
    "shuffle_seed": 1,
    "delivery": "batch",          # "batch" (shuffled) or "immediate"
    "trace_on": True,
    "extra_events": (),           # (time, kind, payload) test hooks
    "est_bin_s": 0.25,
    "est_window_s": 6.0,
    "est_min_bins": 12,
}


class World:
    def __init__(self, config):
        cfg = dict(DEFAULTS)
        cfg.update(config)
        cfg["burst_windows"] = [list(w) for w in cfg["burst_windows"]]
        cfg["loss_seqs"] = [list(k) for k in cfg["loss_seqs"]]
        cfg["extra_events"] = [list(e) for e in cfg["extra_events"]]
        cfg["normal_jitter"] = list(cfg["normal_jitter"])
        cfg["burst_jitter"] = list(cfg["burst_jitter"])
        self.config = cfg

        self.sim = EventSim()
        self.source = Source(cfg["fs"], cfg["packet_len"], cfg["drift_ppm"])
        self.network = Network(
            self.sim, cfg["base_delay_s"], cfg["normal_jitter"],
            cfg["burst_jitter"], cfg["burst_windows"], cfg["loss_prob"],
            cfg["dup_prob"], cfg["loss_seqs"], cfg["seed"],
            cfg["shuffle_seed"], batch=(cfg["delivery"] == "batch"))
        self.estimator = DriftEstimator(
            bin_s=cfg["est_bin_s"], window_s=cfg["est_window_s"],
            min_bins=cfg["est_min_bins"])
        self.controller = DelayController(
            cfg["fs"], base_margin_s=cfg["base_margin_s"],
            init_headroom_s=cfg["init_headroom_s"])
        self.receiver = Receiver(
            cfg["fs"], cfg["packet_len"], cfg["block"], cfg["capacity"],
            self.estimator, self.controller, trace_on=cfg["trace_on"])
        self.network.receiver = self.receiver
        self.tick_period = cfg["block"] / cfg["fs"]

        self.sim.schedule_at(0.0, "send", self.source.generation, prio=0)
        self.sim.schedule_at(self.tick_period, "tick", None, prio=1)
        for event in cfg["extra_events"]:
            t, kind, payload = event[0], event[1], event[2] if len(event) > 2 else None
            self.sim.schedule_at(t, kind, payload, prio=0)

    # ---------------- event handling ----------------

    def dispatch(self, kind, payload):
        now = self.sim.now
        if kind == "send":
            if payload != self.source.generation:
                return  # stale send from before a restart
            pkt = self.source.make_packet(now)
            self.network.send(pkt, now)
            if self.source.next_send_time <= self.config["duration"]:
                self.sim.schedule_at(self.source.next_send_time, "send",
                                     self.source.generation, prio=0)
        elif kind == "arrive":
            self.receiver.on_packet(Packet.from_list(payload))
        elif kind == "tick":
            if self.network.batch:
                self.network.flush(now)
            self.receiver.on_tick(now)
            self.sim.schedule_at(now + self.tick_period, "tick", None, prio=1)
        elif kind == "restart":
            self.source.restart(now)
            self.sim.schedule_at(now, "send", self.source.generation, prio=0)
        elif kind == "set_margin":
            self.controller.base_margin_s = payload
        else:
            raise ValueError("unknown event kind: %r" % (kind,))

    def run(self, until, max_steps=5_000_000):
        steps = 0
        while True:
            t = self.sim.peek_time()
            if t is None or t > until:
                break
            self.sim.step(self.dispatch)
            steps += 1
            if steps > max_steps:
                raise RuntimeError("event loop did not terminate")
        return self

    # ---------------- snapshot / restore ----------------

    def snapshot(self):
        """JSON-safe dict of the complete world state."""
        return {
            "config": self.config,
            "sim": self.sim.to_state(),
            "source": self.source.to_state(),
            "network": self.network.to_state(),
            "estimator": self.estimator.to_state(),
            "controller": self.controller.to_state(),
            "receiver": self.receiver.to_state(),
        }

    @classmethod
    def from_state(cls, state):
        world = cls(state["config"])
        world.sim.load_state(state["sim"])
        world.source.load_state(state["source"])
        world.network.load_state(state["network"])
        world.estimator.load_state(state["estimator"])
        world.controller.load_state(state["controller"])
        world.receiver.load_state(state["receiver"])
        return world
