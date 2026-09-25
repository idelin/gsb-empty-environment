"""Replayable end-to-end sample for the pwlin engine.

``OPS`` is a plain-data operation log (JSON-serialisable).  ``execute`` runs
it against a fresh table and returns a deterministic text transcript;
replaying the same log always reproduces the same transcript, which the
test-suite verifies (including after a JSON round-trip).

Run directly to print the transcript:

    python3.11 -m samples.replay
"""

from __future__ import annotations

import json

from pwlin import CurveTable, Measurement, Node

OPS = [
    ("create", {"channels": ["temp", "pres"]}),
    # temp: non-monotone (0 -> 1.2 -> 0.5 -> 2.3); pres: platform at 10 on [0, 1]
    ("insert", {"x": 0.0, "version": 1, "priority": 0,
                "values": {"temp": [0.0, 0.2], "pres": [10.0, 10.0]}}),
    ("insert", {"x": 1.0, "version": 1, "priority": 0,
                "values": {"temp": [1.0, 1.4], "pres": [10.0, 10.0]}}),
    ("insert", {"x": 2.0, "version": 1, "priority": 0,
                "values": {"temp": [0.5, 0.5]}}),  # pres unknown here: never 0
    ("insert", {"x": 3.0, "version": 1, "priority": 0,
                "values": {"temp": [2.0, 2.6], "pres": [12.0, 12.5]}}),
    ("evaluate", {"channel": "temp", "x": 1.0, "tag": "e_bp"}),
    ("evaluate", {"channel": "temp", "x": 1.5}),
    ("evaluate", {"channel": "pres", "x": 0.5}),
    ("invert", {"channel": "temp", "target": 1.0, "tag": "inv1"}),
    ("invert", {"channel": "pres", "target": 10.0, "tag": "inv_plat"}),
    ("snapshot", {"name": "baseline"}),
    ("calibrate", {"x": 1.0, "channel": "temp", "lo": 1.1, "hi": 1.3, "version": 2}),
    ("valid", {"tag": "e_bp"}),           # revoked by the calibration
    ("evaluate", {"channel": "temp", "x": 1.0, "tag": "e_bp2"}),
    ("valid", {"tag": "e_bp2"}),          # fresh evidence is valid
    ("snapshot_evaluate", {"name": "baseline", "channel": "temp", "x": 1.0}),
    ("snapshot_invert", {"name": "baseline", "channel": "temp", "target": 1.0}),
    ("invert", {"channel": "temp", "target": 1.0, "tag": "inv_pre_delete"}),
    ("delete", {"x": 2.0}),
    ("invert", {"channel": "temp", "target": 1.0}),
    # restore the deleted node verbatim: round-trip must reproduce inv1
    ("insert", {"x": 2.0, "version": 1, "priority": 0,
                "values": {"temp": [0.5, 0.5]}}),
    ("invert", {"channel": "temp", "target": 1.0}),
]


def _fmt_interval(iv) -> str:
    return f"[{iv.lo:.6g},{iv.hi:.6g}]"


def _fmt_sources(nodes) -> str:
    return ",".join(f"(x={n.x:g},v={n.version},p={n.priority})" for n in nodes)


def _fmt_value(ev) -> str:
    return (f"value={_fmt_interval(ev.value)} deriv={_fmt_interval(ev.derivative)}"
            f" bp={ev.at_breakpoint} src={_fmt_sources(ev.sources)}")


def _fmt_inverse(ev) -> str:
    parts = []
    for sol in ev.solutions:
        kind = "interval" if sol.is_interval else "point"
        parts.append(f"{kind}({sol.x_lo:.6g},{sol.x_hi:.6g})")
    return f"target={_fmt_interval(ev.target)} solutions={len(ev.solutions)}: {' '.join(parts)}"


def execute(ops):
    table = None
    snapshots = {}
    evidence = {}
    out = []
    for name, args in ops:
        if name == "create":
            table = CurveTable(args["channels"])
            out.append(f"create channels={list(args['channels'])}")
        elif name == "insert":
            node = Node(x=args["x"], version=args["version"], priority=args["priority"],
                        values={c: Measurement(*v) for c, v in args["values"].items()})
            changed = table.insert(node)
            out.append(f"insert x={args['x']:g} v={args['version']} changed={changed}")
        elif name == "delete":
            changed = table.delete(args["x"])
            out.append(f"delete x={args['x']:g} changed={changed}")
        elif name == "calibrate":
            node = table.calibrate(args["x"], args["channel"], args["lo"], args["hi"],
                                   version=args["version"])
            out.append(f"calibrate x={args['x']:g} {args['channel']} "
                       f"-> [{args['lo']:g},{args['hi']:g}] v={node.version}")
        elif name == "snapshot":
            snapshots[args["name"]] = table.snapshot()
            out.append(f"snapshot {args['name']} epoch={snapshots[args['name']].epoch}")
        elif name == "evaluate":
            ev = table.evaluate(args["channel"], args["x"])
            if "tag" in args:
                evidence[args["tag"]] = (table, ev)
            out.append(f"evaluate {args['channel']}@{args['x']:g} {_fmt_value(ev)}")
        elif name == "snapshot_evaluate":
            snap = snapshots[args["name"]]
            ev = snap.evaluate(args["channel"], args["x"])
            out.append(f"snapshot_evaluate {args['name']} {args['channel']}@"
                       f"{args['x']:g} {_fmt_value(ev)}")
        elif name == "invert":
            ev = table.invert(args["channel"], args["target"])
            if "tag" in args:
                evidence[args["tag"]] = (table, ev)
            out.append(f"invert {args['channel']} {_fmt_inverse(ev)}")
        elif name == "snapshot_invert":
            snap = snapshots[args["name"]]
            ev = snap.invert(args["channel"], args["target"])
            out.append(f"snapshot_invert {args['name']} {args['channel']} {_fmt_inverse(ev)}")
        elif name == "valid":
            holder, ev = evidence[args["tag"]]
            out.append(f"valid {args['tag']} -> {holder.is_valid(ev)}")
        else:
            raise ValueError(f"unknown op {name!r}")
    return out


def main() -> None:
    for line in execute(json.loads(json.dumps(OPS))):
        print(line)


if __name__ == "__main__":
    main()
