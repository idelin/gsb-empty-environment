"""End-to-end demo of the retractable min-max gap network."""

import os
import sys
from datetime import date
from fractions import Fraction

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from temporal import (
    Duration,
    InfeasibleError,
    OffsetTable,
    TemporalNetwork,
    UnknownError,
    WallClock,
)


def fmt(bound):
    lo, hi = bound
    return f"[{lo if lo is not None else '-inf'}, {hi if hi is not None else '+inf'}]"


def section(title):
    print(f"\n=== {title} ===")


# 1. Exact time model: wall clock vs time point vs duration ---------------
section("1. Exact time model + synthetic offset table")
tab = OffsetTable()
tab.define("UTC", Duration.hours(0))
tab.derive("CST", "UTC", Duration.hours(8))               # legal zone
tab.derive("SHIFT", "CST", Duration.minutes(30))          # synthetic: UTC+8:30
wc = WallClock(date(2026, 9, 25), Duration.hours(9), "CST")
tp = tab.to_timepoint(wc)
print(f"wall clock      : {wc}")
print(f"as time point   : {tp} (exact Fraction seconds since epoch)")
wc2 = WallClock(date(2026, 9, 25), Duration.hours(9) + Duration.minutes(30), "SHIFT")
print(f"09:30 in SHIFT  : same instant as 09:00 CST? {tab.to_timepoint(wc2) == tp}")
print(f"duration 1/3h*3 : {(Duration(Fraction(1, 3)) * 3).as_fraction} s exactly (no float)")

# 2. Required infeasible example ------------------------------------------
section("2. B-A in [3,5] and A-B >= 0  ->  infeasible, cycle evidence")
net = TemporalNetwork()
net.add_constraint("A", "B", lower=3, upper=5, cid="gapAB")   # B - A in [3,5]
net.add_constraint("B", "A", lower=0, cid="ordBA")            # A - B >= 0
print(f"status          : {net.status}")
nodes, cids, weight = net.contradiction_detail()
print(f"negative cycle  : nodes={nodes} constraints={cids} weight={weight}")
try:
    net.schedule()
except InfeasibleError as exc:
    print(f"schedule()      : refused -> {exc}")

# 3. Feasible network, push/pop assumptions, history -----------------------
section("3. Push/pop assumptions + history + earliest/latest bounds")
net = TemporalNetwork()
net.set_range("A", 0, 10, cid="rA")
net.add_constraint("A", "B", lower=3, upper=5, cid="gAB")
print(f"bounds B (base) : {fmt(net.bounds('B'))}   (version {net.version})")
v_base = net.version
net.push()
net.add_constraint("A", "B", upper=4, cid="assume-tight")
print(f"bounds B (push) : {fmt(net.bounds('B'))}   (version {net.version})")
net.pop()
print(f"bounds B (pop)  : {fmt(net.bounds('B'))}   (unsupported bound relaxed)")
print(f"history v{v_base}   : B={fmt(net.bounds_at(v_base)['B'])}")
for h in net.history:
    print(f"  v{h.version:<2} {h.op:<22} -> {h.status}")

# 4. Uniform-shift freedom and anchoring -----------------------------------
section("4. Uniform translation freedom, then anchor")
net = TemporalNetwork()
net.add_constraint("A", "B", lower=3, upper=5, cid="gap")
print(f"unanchored A,B  : A={fmt(net.bounds('A'))} B={fmt(net.bounds('B'))} (free shift)")
print(f"witness schedule: {net.schedule()}")
net.set_range("A", 0, 0, cid="pin")
print(f"anchored A=0    : A={fmt(net.bounds('A'))} B={fmt(net.bounds('B'))}")

# 5. Unconstrained events reported separately -------------------------------
section("5. Unconstrained events")
net.add_event("floating")
constrained, unconstrained = net.bounds_all()
print(f"constrained     : { {k: fmt(v) for k, v in constrained.items()} }")
print(f"unconstrained   : {unconstrained}  (range (-inf, +inf))")

# 6. Wall-clock anchored absolute range -------------------------------------
section("6. Absolute range from wall clock via offset table")
net = TemporalNetwork()
t0 = tp.offset.as_fraction
net.set_range("meeting", t0, t0 + 3600, cid="meeting-window")
net.add_constraint("meeting", "standup", lower=900, upper=1800, cid="gap2")
print(f"meeting         : {fmt(net.bounds('meeting'))}")
print(f"standup         : {fmt(net.bounds('standup'))}")

# 7. Budget interruption never returns pseudo-feasible ----------------------
section("7. Budget interruption")
net = TemporalNetwork(budget=40)
prev = "E0"
net.add_event(prev)
for i in range(1, 300):
    cur = f"E{i}"
    net.add_constraint(prev, cur, lower=1, upper=2)
    prev = cur
print(f"status          : {net.status}  (budget 40, chain of 299)")
try:
    net.schedule()
except UnknownError as exc:
    print(f"schedule()      : refused -> {exc}")

# 8. Exact rational conflict (float tolerance would mask it) ----------------
section("8. Exact 1/3-cycle conflict")
net = TemporalNetwork()
third = Fraction(1, 3)
net.add_constraint("A", "B", lower=third, cid="t1")
net.add_constraint("B", "C", lower=third, cid="t2")
net.add_constraint("C", "A", lower=third, cid="t3")
nodes, cids, weight = net.contradiction_detail()
print(f"status          : {net.status}, cycle={cids}, weight={weight} (exactly -1)")
