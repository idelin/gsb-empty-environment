"""Cost demonstration: per-message work is O(1) amortized in the message
count (contiguous-prefix counters + out-of-order sets) and O(S) per command
for the global-minimum recomputation over S sources.  The journal is never
scanned per message — only once at open.

Run:  python3 samples/benchmark.py
"""
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from wmcoord import (
    Ack,
    Data,
    DataMessage,
    Kernel,
    MemoryLog,
    Progress,
    ProgressMessage,
    RegisterSource,
)


def run(sources: int, messages_per_source: int) -> float:
    kernel = Kernel.open(MemoryLog())
    for s in range(sources):
        kernel.submit(RegisterSource(f"src{s:04d}"))
    start = time.perf_counter()
    for s in range(sources):
        name = f"src{s:04d}"
        kernel.submit(Progress(ProgressMessage(name, 1, 10**9, messages_per_source)))
        # Interleave data and acks; every message is O(1) index work plus the
        # O(S) participant scan for the global minimum.
        for seq in range(1, messages_per_source + 1):
            kernel.submit(Data(DataMessage(name, 1, seq, seq)))
            kernel.submit(Ack(name, 1, seq))
    return time.perf_counter() - start


def main():
    print("scaling in message count (sources=64):")
    prev = None
    for messages in (1_000, 2_000, 4_000, 8_000):
        elapsed = run(64, messages)
        ratio = f"{elapsed / prev:.2f}x" if prev else "-"
        total = 64 * messages * 2
        print(f"  messages/source={messages:>6}  commands={total:>7}  "
              f"time={elapsed:6.3f}s  growth={ratio}")
        prev = elapsed

    print("scaling in source count (messages/source=200):")
    for sources in (16, 64, 256):
        elapsed = run(sources, 200)
        print(f"  sources={sources:>4}  time={elapsed:6.3f}s")

    print("recovery is a single journal pass:")
    from wmcoord import FileLog
    import tempfile

    path = os.path.join(tempfile.mkdtemp(), "journal.log")
    log = FileLog(path)
    kernel = Kernel.open(log)
    kernel.submit(RegisterSource("A"))
    kernel.submit(Progress(ProgressMessage("A", 1, 10**9, 20_000)))
    for seq in range(1, 20_001):
        kernel.submit(Data(DataMessage("A", 1, seq, seq)))
        kernel.submit(Ack("A", 1, seq))
    log.close()
    start = time.perf_counter()
    Kernel.open(FileLog(path))
    print(f"  reopen after 40k commands: {time.perf_counter() - start:.3f}s")


if __name__ == "__main__":
    main()
