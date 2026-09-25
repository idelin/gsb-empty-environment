"""Synthetic sample data (all fabricated, no real-world semantics).

A tiny abstract "operations" world: base observations, derivation rules, and
weighted goals.  Designed to exercise sharing (net_ok feeds several goals),
alternative proofs (ticket_needed via two independent paths), conjunction
(g3 needs two conjuncts), and an unreachable goal (g4) for disproof.
"""
from __future__ import annotations

from .model import Fact, Goal, Rule


def build_sample() -> tuple[dict[str, Fact], dict[str, Rule], dict[str, Goal]]:
    facts = {
        f.fact_id: f
        for f in [
            Fact("obs_power"),
            Fact("obs_link"),
            Fact("obs_alarm"),
            Fact("obs_temp"),
        ]
    }
    rules = {
        r.rule_id: r
        for r in [
            Rule("r1", ("obs_power", "obs_link"), "net_ok"),
            Rule("r2", ("net_ok", "obs_alarm"), "svc_degraded"),
            Rule("r3", ("obs_temp",), "thermal_risk"),
            Rule("r4", ("svc_degraded",), "ticket_needed"),
            Rule("r5", ("thermal_risk",), "ticket_needed"),  # alternative proof
            Rule("r6", ("net_ok",), "report_ready"),
            Rule("r7", ("obs_alarm",), "audit_flag"),
        ]
    }
    goals = {
        g.goal_id: g
        for g in [
            Goal("g1", ("ticket_needed",), 5.0),
            Goal("g2", ("report_ready",), 3.0),
            Goal("g3", ("net_ok", "audit_flag"), 4.0),  # conjunction
            Goal("g4", ("never_derivable",), 2.0),      # unreachable
        ]
    }
    return facts, rules, goals
