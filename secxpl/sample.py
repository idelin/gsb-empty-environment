"""Synthetic sample data: a fictional incubator telemetry scenario.

Nothing here is real data.  Levels: 0 = public, 1 = restricted.
"""

from __future__ import annotations

from .model import Fact, Rule, World

TEMP_OK = Fact("temp-reading-ok", 1, 0, "synthetic: 36.9C nominal")
POWER_OK = Fact("power-ok", 1, 0, "synthetic: bus voltage nominal")
CORE_CRITICAL = Fact("core-temp-critical", 2, 1, "synthetic: RESTRICTED core reading")
RAW_ANOMALY = Fact("sensor-raw-anomaly", 1, 1, "synthetic: RESTRICTED raw frame")
OVERRIDE = Fact("manual-override", 1, 1, "synthetic: RESTRICTED operator flag")

UNIVERSE: tuple[Fact, ...] = (TEMP_OK, POWER_OK, CORE_CRITICAL, RAW_ANOMALY, OVERRIDE)

RULES: tuple[Rule, ...] = (
    Rule("R1-nominal", 1, ("temp-reading-ok", "power-ok"), "system-nominal"),
    Rule("R2-shutdown", 1, ("system-nominal", "core-temp-critical"), "shutdown-required"),
    Rule("R3-derive-critical", 1, ("sensor-raw-anomaly",), "core-temp-critical"),
    Rule("R4-override", 1, ("manual-override",), "shutdown-required"),
)

QUERIES: tuple[str, ...] = (
    "temp-reading-ok",
    "power-ok",
    "system-nominal",
    "shutdown-required",
    "core-temp-critical",
    "sensor-raw-anomaly",
    "manual-override",
    "nonexistent-id",
)


def make_world(present_ids) -> World:
    ids = set(present_ids)
    return World(
        facts=tuple(f for f in UNIVERSE if f.fact_id in ids),
        rules=RULES,
    )


def all_worlds() -> list[World]:
    """The finite test domain: every subset of the fact universe."""
    ids = [f.fact_id for f in UNIVERSE]
    worlds = []
    for mask in range(1 << len(ids)):
        worlds.append(make_world(ids[i] for i in range(len(ids)) if mask & (1 << i)))
    return worlds
