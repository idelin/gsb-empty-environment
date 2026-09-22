#!/usr/bin/env python3
from __future__ import annotations

import tempfile
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from watermark_kernel import (
    CoordinatorNode,
    DataMessage,
    DownstreamNode,
    ProgressMessage,
    Simulation,
    WatermarkKernel,
)
from watermark_kernel.errors import SimulatedProcessCrash


def add_source(kernel, source, epoch):
    kernel.proposal(source, epoch)
    kernel.accept_source(source, epoch, kernel.global_boundary)


def replay_basic_case(directory):
    kernel = WatermarkKernel.open(directory, checkpoint_every=1)
    add_source(kernel, "A", "epoch-A")
    add_source(kernel, "B", "epoch-B")
    kernel.progress(ProgressMessage("A", "epoch-A", 100, 3))
    kernel.data(DataMessage("A", "epoch-A", 1, 1))
    kernel.data(DataMessage("A", "epoch-A", 3, 3))
    kernel.progress(ProgressMessage("B", "epoch-B", 80, 0))
    trace = [("gap: 1 and 3 arrived", kernel.boundary)]
    kernel.data(DataMessage("A", "epoch-A", 2, 2))
    trace.append(("gap filled but not acked", kernel.boundary))
    for seq in (1, 2, 3):
        kernel.ack("A", "epoch-A", seq)
    trace.append(("A cut fully acknowledged", kernel.boundary))
    kernel.mark_idle("B")
    trace.append(("B idle", kernel.boundary))
    return trace


def replay_handover_case(directory):
    kernel = WatermarkKernel.open(directory, checkpoint_every=1)
    add_source(kernel, "parent", "epoch-parent")
    kernel.progress(ProgressMessage("parent", "epoch-parent", 100, 2))
    for seq in (1, 2):
        kernel.data(DataMessage("parent", "epoch-parent", seq, seq))
        kernel.ack("parent", "epoch-parent", seq)
    kernel.begin_handover(
        "split-at-100",
        "parent",
        "epoch-parent",
        100,
        2,
        [("child-a", "epoch-child-a"), ("child-b", "epoch-child-b")],
    )
    trace = [("handover registered", kernel.boundary, sorted(kernel.sources))]
    kernel.confirm_child("split-at-100", "child-a", "epoch-child-a")
    trace.append(("one child confirmed", kernel.boundary, sorted(kernel.sources)))
    kernel.confirm_child("split-at-100", "child-b", "epoch-child-b")
    trace.append(("both children confirmed", kernel.boundary, sorted(kernel.sources)))
    return trace


def fault_once(phase):
    def hook(current_phase, event):
        if not hook.fired and current_phase == phase and event.get("boundary") == 80:
            hook.fired = True
            raise SimulatedProcessCrash(phase)

    hook.fired = False
    return hook


def replay_crash_case(directory, phase):
    downstream = DownstreamNode()
    hook = fault_once(phase)
    coordinator = CoordinatorNode(directory, downstream, fault_hook=hook, checkpoint_every=1)
    simulation = Simulation()
    simulation.start_coordinator(coordinator)
    for source, epoch in [("A", "epoch-A"), ("B", "epoch-B")]:
        simulation.send(coordinator, "proposal", {"source_id": source, "epoch": epoch})
        simulation.send(
            coordinator,
            "accept_source",
            {"source_id": source, "epoch": epoch, "accepted_floor": 0},
        )
    simulation.send(
        coordinator,
        "progress",
        {"source": "A", "epoch": "epoch-A", "frontier": 100, "cut_seq": 0},
        delay=2,
    )
    simulation.send(
        coordinator,
        "progress",
        {"source": "B", "epoch": "epoch-B", "frontier": 80, "cut_seq": 0},
        delay=3,
    )
    simulation.run()
    decisions = [(decision.decision_id, decision.boundary) for decision in downstream.received]
    return coordinator.restart_count, decisions


def main():
    with tempfile.TemporaryDirectory() as basic_dir, tempfile.TemporaryDirectory() as handover_dir:
        print("basic replay")
        for label, boundary in replay_basic_case(basic_dir):
            print(f"  {label}: boundary={boundary}")
        print("partition replay")
        for label, boundary, participants in replay_handover_case(handover_dir):
            print(f"  {label}: boundary={boundary}, participants={','.join(participants)}")
    for phase in (
        "decision_pre_append",
        "decision_post_append_pre_publish",
        "decision_post_publish_pre_ack",
    ):
        with tempfile.TemporaryDirectory() as crash_dir:
            restarts, decisions = replay_crash_case(crash_dir, phase)
            print(f"crash {phase}: restarts={restarts}, decisions={decisions}")


if __name__ == "__main__":
    main()
