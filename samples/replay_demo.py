"""Replay demo: drive the canonical scenarios through the durable kernel,
crash it, and recover — printing the journal, decisions and closures.

Run:  python3 samples/replay_demo.py
"""
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from wmcoord import (
    Ack,
    BeginHandoff,
    ChildTakeover,
    Data,
    DataMessage,
    FileLog,
    IdleTimeout,
    Kernel,
    Progress,
    ProgressMessage,
    RegisterSource,
)


def show(kernel, note):
    pending = [
        f"{did}@{boundary}({status})"
        for did, boundary, status in kernel.pending_decisions()
    ]
    print(f"  boundary={kernel.boundary:<4} pending={pending}   <- {note}")


def main():
    workdir = tempfile.mkdtemp(prefix="wmcoord-demo-")
    journal_path = os.path.join(workdir, "journal.log")
    checkpoint_path = os.path.join(workdir, "checkpoint.bin")
    print(f"workdir: {workdir}\n")

    print("== Scenario 1: gap, ack, idle ==")
    kernel = Kernel.open(FileLog(journal_path), checkpoint_path=checkpoint_path)
    kernel.submit(RegisterSource("A"))
    kernel.submit(RegisterSource("B"))
    kernel.submit(Progress(ProgressMessage("A", 1, 100, 3)))
    kernel.submit(Data(DataMessage("A", 1, 1, 10)))
    kernel.submit(Data(DataMessage("A", 1, 3, 30)))
    kernel.submit(Progress(ProgressMessage("B", 1, 80, 0)))
    show(kernel, "A reports frontier=100 cut=3 but seq 2 is missing: blocked")
    kernel.submit(Data(DataMessage("A", 1, 2, 20)))
    show(kernel, "seq 2 arrived but not acknowledged: still blocked")
    for seq in (1, 2, 3):
        kernel.submit(Ack("A", 1, seq))
    show(kernel, "all of 1..3 acknowledged: advance to min(100, 80) = 80")
    kernel.submit(IdleTimeout("B"))
    show(kernel, "B idle with nothing pending: advance to 100")

    print("\n== Checkpoint, crash, recover ==")
    kernel.save_checkpoint()
    print("  checkpoint written; killing process (kernel object discarded)")
    del kernel
    kernel = Kernel.open(FileLog(journal_path), checkpoint_path=checkpoint_path)
    show(kernel, "recovered from checkpoint + journal tail")

    print("\n== Handoff: A splits into C1, C2 at H=100 ==")
    kernel.submit(BeginHandoff("A", "h1", 100, children=("C1", "C2")))
    kernel.submit(ChildTakeover("h1", "C1", 1, 100))
    show(kernel, "only C1 confirmed: handoff pending, A still participates")
    result = kernel.submit(ChildTakeover("h1", "C2", 1, 100))
    show(kernel, f"C2 confirmed: handoff {result.handoff_completed} complete")
    late = kernel.submit(Data(DataMessage("A", 1, 4, 55)))
    print(f"  old-parent data after handoff: {late.disposition} "
          f"(late channel size={len(kernel.state['late'])})")
    kernel.submit(Progress(ProgressMessage("C1", 1, 150, 0)))
    kernel.submit(Progress(ProgressMessage("C2", 1, 120, 0)))
    show(kernel, "children report frontiers 150/120: advance to 120")

    print("\n== Publish pending decisions to a deduplicating downstream ==")
    seen = set()
    for did, boundary, _status in kernel.pending_decisions():
        kernel.publish(did, boundary)
        if did in seen:
            print(f"  downstream: duplicate {did} ignored")
            continue
        seen.add(did)
        print(f"  downstream: CLOSE window at {boundary}  ({did})")
        kernel.confirm(did)
    show(kernel, "all decisions confirmed")

    print("\n== Final reopen: state survives a clean restart ==")
    del kernel
    kernel = Kernel.open(FileLog(journal_path), checkpoint_path=checkpoint_path)
    show(kernel, "reopened")
    print(f"\njournal size: {os.path.getsize(journal_path)} bytes, "
          f"checkpoint: {os.path.getsize(checkpoint_path)} bytes")


if __name__ == "__main__":
    main()
