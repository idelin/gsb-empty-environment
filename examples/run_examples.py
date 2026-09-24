#!/usr/bin/env python3.11
"""Runnable demos for minivm.

Usage:  python3.11 examples/run_examples.py
"""

import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minivm import DONE, Machine, compile_program, snapshot  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))


def load(name):
    with open(os.path.join(HERE, name), encoding="utf-8") as fh:
        return fh.read()


def demo_counter_snapshot():
    print("== counter + snapshot/restore ==")
    program = compile_program(load("counter.mn"))
    machine = Machine(program)
    co = machine.start()
    print("yielded:", co.resume())

    # Serialize the suspended coroutine to a JSON file...
    path = os.path.join(tempfile.gettempdir(), "minivm-counter-snapshot.json")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(snapshot.dumps(co, indent=2))
    print("snapshot written to", path)

    # ...and resume it on a fresh machine from the same program.
    with open(path, encoding="utf-8") as fh:
        data = fh.read()
    restored = snapshot.restore(Machine(program), data)
    out = restored.resume()
    while out is not DONE:
        out = restored.resume()
    print("restored machine output:", *restored.machine.output)
    print("restored final result:", restored.result)

    # A changed program is rejected.
    changed = Machine(compile_program(load("counter.mn") + "\n42\n"))
    try:
        snapshot.restore(changed, data)
        print("ERROR: changed program was accepted!")
    except Exception as exc:
        print("changed program rejected:", exc)
    print()


def demo_loop_closures():
    print("== loop closures are independent ==")
    machine = Machine(compile_program(load("loop_closures.mn")))
    co = machine.start()
    co.resume()
    print("output:", *machine.output)
    print()


def demo_finally_cleanup():
    print("== nested finally runs exactly once on close ==")
    machine = Machine(compile_program(load("finally_cleanup.mn")))
    co = machine.start()
    print("yielded:", co.resume())
    co.close()
    print("output after close:", *machine.output)
    print("status:", co.status)
    print()


if __name__ == "__main__":
    demo_counter_snapshot()
    demo_loop_closures()
    demo_finally_cleanup()
