"""Greedy 1-minimization of counterexample scenarios under a replay budget.

A scenario is shrunk by deleting single elements -- one action from the
trace, one key from the initial-config overrides, one fault from the fault
set.  A deletion candidate is accepted only when the shortened scenario
still *independently replays* to a violation with the *same error
signature*.  In particular:

* candidates whose replay is INVALID (an action precondition no longer
  holds) are rejected;
* candidates that replay to a *different* error signature are rejected
  (a different error is a different counterexample);
* candidates that replay CLEAN are rejected.

When the fixpoint pass completes, every single deletion has been tested and
rejected: the result is *locally* 1-minimal.  This is never a claim of
global minimality.  If the replay budget is exhausted first, the report
says so explicitly and does not claim minimality at all.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from model import Model, VIOLATION, replay


@dataclass
class Scenario:
    """A replayable scenario: config overrides + action trace + fault set."""

    config: dict
    actions: list
    faults: frozenset

    def copy(self) -> "Scenario":
        return Scenario(dict(self.config), list(self.actions), frozenset(self.faults))

    def describe(self) -> str:
        lines = [
            f"  config : {dict(sorted(self.config.items(), key=lambda kv: kv[0])) or '{}'}",
            f"  actions: [{', '.join(self.actions)}]",
            f"  faults : {{{', '.join(sorted(self.faults))}}}",
        ]
        return "\n".join(lines)


@dataclass
class ShrinkReport:
    scenario: Scenario
    error: str
    replays_used: int
    budget: int
    locally_minimal: bool      # True only when the fixpoint pass completed
    budget_exhausted: bool
    log: list = field(default_factory=list)

    def summary(self) -> str:
        lines = [
            f"error signature : {self.error}",
            f"replays used    : {self.replays_used}/{self.budget}",
        ]
        if self.locally_minimal:
            lines.append(
                "minimality      : locally 1-minimal -- every single deletion "
                "(action/config/fault) was tested and fails to reproduce "
                f"'{self.error}'. This is NOT a claim of global minimality."
            )
        else:
            lines.append(
                "minimality      : NOT confirmed -- replay budget exhausted "
                "before the fixpoint pass completed; the scenario may still "
                "be shrinkable. No minimality (local or global) is claimed."
            )
        lines.append("scenario:")
        lines.append(self.scenario.describe())
        return "\n".join(lines)


class _BudgetExhausted(Exception):
    pass


class Shrinker:
    """Shrinks a scenario while preserving one specific error signature."""

    def __init__(self, model: Model, budget: int = 1000):
        if budget < 1:
            raise ValueError("budget must be >= 1")
        self.model = model
        self.budget = budget
        self.replays_used = 0

    def _replay_counted(self, scenario: Scenario):
        if self.replays_used >= self.budget:
            raise _BudgetExhausted
        self.replays_used += 1
        return replay(self.model, scenario.config, scenario.actions, scenario.faults)

    def _reproduces(self, scenario: Scenario, target_error: str) -> bool:
        result = self._replay_counted(scenario)
        return result.status == VIOLATION and result.error == target_error

    def shrink(self, scenario: Scenario) -> ShrinkReport:
        initial = self._replay_counted(scenario)
        if initial.status != VIOLATION:
            raise ValueError(
                f"scenario does not replay to a violation (status={initial.status}); "
                "only replayable counterexamples can be shrunk"
            )
        target = initial.error
        current = scenario.copy()
        log = []
        exhausted = False
        try:
            changed = True
            while changed:
                changed = False

                index = 0
                while index < len(current.actions):
                    candidate = Scenario(
                        dict(current.config),
                        current.actions[:index] + current.actions[index + 1:],
                        current.faults,
                    )
                    if self._reproduces(candidate, target):
                        log.append(f"drop action[{index}] {current.actions[index]!r}")
                        current = candidate
                        changed = True
                    else:
                        index += 1

                for key in list(current.config):
                    cand_config = dict(current.config)
                    del cand_config[key]  # falls back to the model default
                    candidate = Scenario(cand_config, list(current.actions), current.faults)
                    if self._reproduces(candidate, target):
                        log.append(f"reset config[{key!r}] to model default")
                        current = candidate
                        changed = True

                for fault in sorted(current.faults):
                    candidate = Scenario(
                        dict(current.config),
                        list(current.actions),
                        current.faults - {fault},
                    )
                    if self._reproduces(candidate, target):
                        log.append(f"drop fault {fault!r}")
                        current = candidate
                        changed = True
        except _BudgetExhausted:
            exhausted = True
            log.append("replay budget exhausted; stopping before fixpoint")

        return ShrinkReport(
            scenario=current,
            error=target,
            replays_used=self.replays_used,
            budget=self.budget,
            locally_minimal=not exhausted,
            budget_exhausted=exhausted,
            log=log,
        )
