"""Finite-state exploration.

Enumerates legal delivery/confirmation orders of small message sets and runs
each ordering through both the production kernel and the independent
reference checker (tests/reference_checker.py, which never imports the
production code).  For every step we require:

* identical disposition (accepted / ignored / late / protocol-error);
* identical global boundary (never regressing);
* identical durable decision sequences (stable IDs, same order);
* identical late-channel classifications;
* no safety violation reported by the reference checker (unconfirmed data
  never sealed over; partitions never lost).

After each ordering we also reopen the kernel from its journal (simulating a
crash + restart) and require the recovered state to still agree.
"""
import itertools
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from wmcoord import (
    Ack,
    BeginHandoff,
    ChildTakeover,
    Data,
    DataMessage,
    IdleTimeout,
    JoinSource,
    Kernel,
    MemoryLog,
    Progress,
    ProgressMessage,
    ProtocolError,
    RegisterSource,
)
from reference_checker import RefChecker, RefError


def run_ordering(commands, register=()):
    """Run one ordering through both implementations and compare."""
    log = MemoryLog()
    kernel = Kernel.open(log)
    ref = RefChecker()
    for name in register:
        kernel.submit(RegisterSource(name))
        ref.step(RegisterSource(name))
    prev_boundary = 0
    for command in commands:
        prod_error = ref_error = None
        try:
            result = kernel.submit(command)
        except ProtocolError as exc:
            prod_error = exc
            result = None
        try:
            disposition = ref.step(command)
        except RefError as exc:
            ref_error = exc
            disposition = None
        if (prod_error is None) != (ref_error is None):
            raise AssertionError(
                f"error mismatch on {command}: prod={prod_error} ref={ref_error}"
            )
        if result is not None and result.disposition != disposition:
            raise AssertionError(
                f"disposition mismatch on {command}: "
                f"prod={result.disposition} ref={disposition}"
            )
        if kernel.boundary != ref.boundary:
            raise AssertionError(
                f"boundary mismatch after {command}: "
                f"prod={kernel.boundary} ref={ref.boundary}"
            )
        if kernel.boundary < prev_boundary:
            raise AssertionError(f"boundary regressed at {command}")
        prev_boundary = kernel.boundary
        prod_decisions = [
            (did, info["boundary"])
            for did, info in kernel.state["decisions"].items()
        ]
        if prod_decisions != ref.decisions:
            raise AssertionError(
                f"decision mismatch after {command}: "
                f"prod={prod_decisions} ref={ref.decisions}"
            )
    if ref.violations:
        raise AssertionError(f"safety violations: {ref.violations}")
    # Late channel classifications must agree.
    prod_late = [reason for reason, _ in kernel.state["late"]]
    ref_late = [reason for reason, _ in ref.late]
    if prod_late != ref_late:
        raise AssertionError(
            f"late channel mismatch: prod={prod_late} ref={ref_late}"
        )
    # Crash + restart: recovery must preserve agreement.
    recovered = Kernel.open(log)
    if recovered.boundary != ref.boundary:
        raise AssertionError(
            f"recovered boundary {recovered.boundary} != {ref.boundary}"
        )
    recovered_decisions = [
        (did, info["boundary"])
        for did, info in recovered.state["decisions"].items()
    ]
    if recovered_decisions != ref.decisions:
        raise AssertionError("recovered decisions diverge")
    return kernel, ref


class TestGapAckExploration(unittest.TestCase):
    def test_all_orderings_of_gap_scenario(self):
        commands = [
            Progress(ProgressMessage("A", 1, 100, 2)),
            Data(DataMessage("A", 1, 1, 10)),
            Data(DataMessage("A", 1, 2, 20)),
            Ack("A", 1, 1),
            Ack("A", 1, 2),
            Progress(ProgressMessage("B", 1, 80, 0)),
            IdleTimeout("B"),
        ]
        count = 0
        for ordering in itertools.permutations(commands):
            run_ordering(list(ordering), register=("A", "B"))
            count += 1
        self.assertEqual(count, 5040)

    def test_join_and_idle_orderings(self):
        commands = [
            Progress(ProgressMessage("A", 1, 50, 0)),
            JoinSource("B", 0),  # only legal before A's progress seals
            Progress(ProgressMessage("B", 1, 70, 0)),
            IdleTimeout("A"),
        ]
        for ordering in itertools.permutations(commands):
            run_ordering(list(ordering), register=("A",))

    def test_epoch_and_late_orderings(self):
        commands = [
            Progress(ProgressMessage("A", 1, 50, 0)),
            Progress(ProgressMessage("A", 2, 60, 0)),
            Data(DataMessage("A", 1, 1, 10)),
            Data(DataMessage("A", 2, 1, 70)),
            Ack("A", 2, 1),
        ]
        for ordering in itertools.permutations(commands):
            run_ordering(list(ordering), register=("A",))


class TestHandoffExploration(unittest.TestCase):
    def test_all_orderings_of_handoff_scenario(self):
        commands = [
            Progress(ProgressMessage("P", 1, 100, 1)),
            Data(DataMessage("P", 1, 1, 50)),
            Ack("P", 1, 1),
            BeginHandoff("P", "h1", 100, children=("C1", "C2")),
            ChildTakeover("h1", "C1", 1, 100),
            ChildTakeover("h1", "C2", 1, 100),
        ]
        completed = 0
        for ordering in itertools.permutations(commands):
            kernel, ref = run_ordering(list(ordering), register=("P",))
            handoff = kernel.state["handoffs"]["h1"]
            if handoff["status"] == "done":
                completed += 1
                # Partitions not lost: parent exited, both children active.
                self.assertEqual(kernel.state["sources"]["P"]["status"], "exited")
                for child in ("C1", "C2"):
                    self.assertEqual(
                        kernel.state["sources"][child]["status"], "active"
                    )
                    self.assertGreaterEqual(
                        kernel.state["sources"][child]["water"], 100
                    )
                self.assertEqual(ref.sources["P"].status, "exited")
        self.assertGreater(completed, 0)


if __name__ == "__main__":
    unittest.main()
