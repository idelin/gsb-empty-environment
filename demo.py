"""End-to-end demo:
1. basic linear resampling + breakpoint + retraction (the spec's example);
2. a consumer that maintains a full grid purely by applying revisions;
3. random chunked ingestion vs whole-segment reference;
4. closing an interval and releasing history.
"""
import random
from decimal import Decimal

from resampler import Resampler, compute_reference, MODE_LINEAR, MODE_STEP


def show(points):
    for p in points:
        v = str(p.value) if p.valid else f"unknown({p.reason})"
        print(f"  k={p.index:>2} t={str(p.t):>4} value={v:<24} ids={list(p.sample_ids)}")


print("=== 1. spec example: (0,0),(4,8), linear, dt=1 ===")
r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
r.add_sample(0, 0, sample_id="a")
r.add_sample(4, 8, sample_id="b")
show(r.grid_range(0, 4))

print("--- add explicit breakpoint at t=2: grid 2 must NOT be 4 ---")
r.add_breakpoint(2)
show(r.grid_range(0, 4))

print("--- remove breakpoint, then retract right endpoint 'b' ---")
r.remove_breakpoint(2)
events = r.retract("b")
for e in events:
    print(f"  revision v{e.version}: k={e.index} "
          f"{e.old.value if e.old else None} -> "
          f"{e.new.value if e.new and e.new.valid else 'unknown'}")
show(r.grid_range(0, 4))

print()
print("=== 2. consumer restores full grid from revisions ===")
r2 = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
r2.add_sample(0, 0, sample_id="a")
r2.add_sample(10, 100, sample_id="b")


class GridConsumer:
    """Holds a materialized grid; only applies revision events afterwards."""

    def __init__(self, src, k0, k1):
        self.src, self.k0, self.k1 = src, k0, k1
        self.cells = {p.index: p for p in src.grid_range(k0, k1)}
        self.version = src.version

    def catch_up(self):
        n = 0
        for e in self.src.revisions_since(self.version):
            self.cells[e.index] = e.new
            self.version = max(self.version, e.version)
            n += 1
        return n


c = GridConsumer(r2, 0, 10)
print("initial grid at k=5:", c.cells[5].value)
r2.add_sample(5, 55, sample_id="late")     # late arrival
r2.retract("b")                            # retraction
applied = c.catch_up()
print(f"applied {applied} revision events; grid now matches fresh query:",
      all(c.cells[p.index] == p for p in r2.grid_range(0, 10)))
show([c.cells[k] for k in range(0, 11)])

print()
print("=== 3. random chunks vs whole-segment reference ===")
rng = random.Random(7)
for mode in (MODE_STEP, MODE_LINEAR):
    samples = [(rng.randint(-30, 30) / 2, rng.randint(0, 100) / 3, f"id{i}")
               for i in range(40)]
    breaks = [rng.randint(-30, 30) / 2 for _ in range(3)]
    order = samples[:]
    rng.shuffle(order)
    chunks = [order[i:i + 5] for i in range(0, len(order), 5)]
    rng.shuffle(chunks)
    arrival = [s for ch in chunks for s in ch]

    cfg = dict(dt="0.5", mode=mode, max_gap=6, t0="-0.25")
    ref = compute_reference(arrival, breaks, k0=-60, k1=60, **cfg)

    rc = Resampler(**cfg)
    for b in breaks:
        rc.add_breakpoint(b)
    for ch in chunks:
        for s in ch:
            rc.add_sample(*s)
    got = rc.grid_range(-60, 60)
    print(f"  mode={mode:<6} chunked == reference: {got == ref}")

print()
print("=== 4. close interval & release history ===")
r4 = Resampler(dt=1, mode=MODE_STEP, max_gap=50)
for t in range(0, 20):
    r4.add_sample(t, t * 10, sample_id=f"s{t}")
r4.grid_range(0, 25)
info = r4.close_until(12)
print("  close_until(12) ->", {k: (str(v) if k == "anchor" else v)
                              for k, v in info.items()})
try:
    r4.add_sample(5, 0)  # late arrival into a closed interval
except ValueError as exc:
    print("  late sample at t=5 rejected:", exc)
print("  grid after close still correct:",
      [str(p.value) for p in r4.grid_range(12, 15)])
