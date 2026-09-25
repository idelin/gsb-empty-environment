"""Synthetic sample worlds.  All data below is fabricated for
demonstration and testing only; it refers to no real persons or systems.

Scenario: a synthetic loan-approval rule.  Two public base facts
(income and identity verified) plus a hidden risk-screening chain of
variable length feed one public approval rule.  Worlds differ ONLY in
hidden parts: the length of the hidden screening chain and the number of
unused hidden note facts.
"""
from __future__ import annotations

from .model import PUBLIC, SECRET, Fact, Rule
from .noninterference import World


def build_world(chain_len: int = 2, extra_hidden: int = 0,
                name: str | None = None) -> World:
    facts = [
        Fact("F-INCOME-001", 1, "income_verified", "true", PUBLIC),
        Fact("F-ID-001", 1, "id_verified", "true", PUBLIC),
        Fact("F-SCREEN-000", 1, "screen_0", "clear", SECRET),
    ]
    rules: list[Rule] = []
    for i in range(1, chain_len + 1):
        conclusion = "risk_cleared" if i == chain_len else f"screen_{i}"
        rules.append(Rule(f"R-CHAIN-{i:03d}", 1, (f"screen_{i - 1}",),
                          conclusion, SECRET, SECRET))
    for j in range(extra_hidden):
        facts.append(Fact(f"F-NOTE-{j:03d}", 1, f"note_{j}", "redacted",
                          SECRET))
    rules.append(Rule("R-APPROVE-001", 2,
                      ("income_verified", "id_verified", "risk_cleared"),
                      "approved", PUBLIC, PUBLIC))
    return World(name or f"chain{chain_len}-extra{extra_hidden}",
                 tuple(facts), tuple(rules))


def sample_domain() -> list[World]:
    """Finite domain of low-equivalent worlds for the tester."""
    return [build_world(chain_len=c, extra_hidden=e)
            for c in (1, 2, 3, 4) for e in (0, 1, 2)]
