"""Fault-injection equivalence.

For identical logical inputs and timeout histories, the downstream closure
sequence (after ID de-duplication) of a run recovering from crashes must be
identical to the fault-free run.  Commands are spaced 4 virtual-time units
apart; crashes are injected in the gaps so no unrelated message is lost.

Covered crash points:
* after the command is durable but before the decision record
  (``after_command_write``);
* after the decision is durable but before publish (``after_decision_write``);
* after publish but before downstream confirm (``after_publish_write`` and a
  timed crash while the confirm is in flight);
* around the handoff-completion record (``before/after_handoff_write``);
* before the command itself is durable (``before_command_write``), where the
  driver re-submits the lost command after restart.
"""
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
    MemoryLog,
    Progress,
    ProgressMessage,
    RegisterSource,
)
from wmcoord.simulator import CoordinatorNode, DownstreamNode, Driver, Simulator


def scenario_commands():
    """The logical input script shared by every fault variant."""
    return [
        (4, RegisterSource("A")),
        (8, RegisterSource("B")),
        (12, Progress(ProgressMessage("A", 1, 100, 3))),
        (16, Data(DataMessage("A", 1, 1, 10))),
        (20, Data(DataMessage("A", 1, 3, 30))),
        (24, Progress(ProgressMessage("B", 1, 80, 0))),
        (28, Data(DataMessage("A", 1, 2, 20))),
        (32, Ack("A", 1, 1)),
        (36, Ack("A", 1, 2)),
        (40, Ack("A", 1, 3)),          # seals D1 = 80
        (44, IdleTimeout("B")),        # seals D2 = 100
        (48, BeginHandoff("A", "h1", 100, children=("C1", "C2"))),
        (52, ChildTakeover("h1", "C1", 1, 100)),
        (56, ChildTakeover("h1", "C2", 1, 100)),  # HANDOFF_DONE
        (60, Data(DataMessage("A", 1, 4, 55))),   # old parent -> late
        (64, Progress(ProgressMessage("C1", 1, 150, 0))),
        (68, Progress(ProgressMessage("C2", 1, 120, 0))),  # seals D3 = 120
    ]


def run_script(faults=(), extra=None):
    """Run the scenario with fault actions ``(time, hook|crash|restart)``."""
    sim = Simulator()
    log = MemoryLog()
    coord = CoordinatorNode(sim, lambda: log)
    down = DownstreamNode(sim)
    driver = Driver(sim)
    for time, command in scenario_commands():
        driver.at(time, command)
    for time, action in faults:
        if action == "crash":
            driver.crash_at(time)
        elif action == "restart":
            driver.restart_at(time)
        else:
            driver.arm_at(time, action)
    if extra:
        extra(driver)
    sim.run()
    return down, coord


class TestFaultEquivalence(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.baseline, _ = run_script()
        cls.expected = cls.baseline.closed
        # Sanity: the fault-free run seals 80, 100, 120 in order.
        assert [b for _, b in cls.expected] == [80, 100, 120], cls.expected

    def assert_equivalent(self, down, why):
        self.assertEqual(
            down.closed,
            self.expected,
            f"{why}: deduped closure sequence diverged\n"
            f"  got:      {down.closed}\n"
            f"  expected: {self.expected}",
        )

    def test_crash_after_command_write_before_decision(self):
        # Decision record not yet durable; recovery must repair it with the
        # same ID instead of minting a new one.
        down, _ = run_script(
            faults=[(39.5, "after_command_write"), (40.5, "restart")]
        )
        self.assert_equivalent(down, "crash after command write")

    def test_crash_after_decision_write_before_publish(self):
        down, _ = run_script(
            faults=[(39.5, "after_decision_write"), (40.5, "restart")]
        )
        self.assert_equivalent(down, "crash after decision write")

    def test_crash_after_publish_before_confirm(self):
        down, _ = run_script(
            faults=[(39.5, "after_publish_write"), (40.5, "restart")]
        )
        self.assert_equivalent(down, "crash after publish write")

    def test_crash_while_confirm_in_flight(self):
        # D1 delivered downstream at t=41; coordinator dies at t=41.5 before
        # the confirm arrives at t=42.  Restart re-publishes the same ID.
        down, _ = run_script(faults=[(41.5, "crash"), (42.5, "restart")])
        self.assert_equivalent(down, "crash with confirm in flight")

    def test_crash_before_handoff_record(self):
        down, _ = run_script(
            faults=[(55.5, "before_handoff_write"), (56.5, "restart")]
        )
        self.assert_equivalent(down, "crash before handoff record")

    def test_crash_after_handoff_record(self):
        down, _ = run_script(
            faults=[(55.5, "after_handoff_write"), (56.5, "restart")]
        )
        self.assert_equivalent(down, "crash after handoff record")

    def test_crash_before_command_write_with_driver_retry(self):
        # The command is lost entirely; the driver re-submits it at t=30.
        lost = Data(DataMessage("A", 1, 2, 20))

        def extra(driver):
            driver.at(30, lost)

        down, _ = run_script(
            faults=[(27.5, "before_command_write"), (28.5, "restart")],
            extra=extra,
        )
        self.assert_equivalent(down, "crash before command write + retry")

    def test_repeated_crashes(self):
        down, _ = run_script(
            faults=[
                (39.5, "after_decision_write"),
                (40.5, "restart"),
                (55.5, "before_handoff_write"),
                (56.5, "restart"),
                (67.5, "after_publish_write"),
                (68.5, "restart"),
            ]
        )
        self.assert_equivalent(down, "repeated crashes")

    def test_crash_does_not_create_duplicate_decision_ids(self):
        # Confirm is lost in flight, so the restart re-publishes D1 and the
        # downstream really does see it twice; de-duplication by stable ID
        # keeps the closure sequence identical to the baseline.
        down, _ = run_script(faults=[(41.5, "crash"), (42.5, "restart")])
        ids = [did for did, _ in down.closed]
        self.assertEqual(len(ids), len(set(ids)))
        self.assertGreaterEqual(down.duplicates, 1)  # re-publish happened


if __name__ == "__main__":
    unittest.main()
