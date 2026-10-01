"""Runnable samples for the handle-state verifier.

Run from the repository root:

    python3.11 examples/run_samples.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from handle_verifier import (EXCLUSIVE, READ, SHARED, WRITE, Analyzer,
                             Assign, Borrow, Call, Close, EndBorrow, Function,
                             If, Move, Open, Program, Return, Use, While,
                             replay)


def show(title, program):
    result = Analyzer(program).analyze()
    print(f"== {title}")
    print(result)
    for record in result.errors:
        ok = replay(program, record)
        print(f"  replay {record.kind.value}: {'OK' if ok else 'FAILED'}"
              f" (choices={record.choices})")
    print()
    return result


def main():
    show("safe: shared reads, exclusive write, borrow returned",
         Program({"main": Function("main", (), (
             Open("x"),
             Borrow("b", "x", SHARED),
             Borrow("c", "x", SHARED),
             Use("b", READ),
             Use("c", READ),
             EndBorrow("b"),
             EndBorrow("c"),
             Borrow("w", "x", EXCLUSIVE),
             Use("w", WRITE),
             EndBorrow("w"),
             Close("x"),
         ))}))

    show("possible error: closed on one branch, used after the join",
         Program({"main": Function("main", (), (
             Open("x"),
             If((Close("x"),), ()),
             Use("x", READ),
         ))}))

    show("error: closed while borrowed",
         Program({"main": Function("main", (), (
             Open("x"),
             Borrow("b", "x", SHARED),
             Close("x"),
         ))}))

    show("error: alias moved, owner used (aliases share one identity)",
         Program({"main": Function("main", (), (
             Open("x"),
             Assign("y", "x"),
             Move("y"),
             Use("x", WRITE),
         ))}))

    show("error: loop may close, use after the loop",
         Program({"main": Function("main", (), (
             Open("x"),
             While((Close("x"),)),
             Use("x", READ),
         ))}))

    show("cross-function transfer with recursion fixpoint",
         Program({
             "rec": Function("rec", ("p",), (
                 If(
                     (Close("p"), Return("p")),
                     (Call("r", "rec", ("p",)), Return("r")),
                 ),
             )),
             "main": Function("main", (), (
                 Open("x"),
                 Call("y", "rec", ("x",)),
                 Use("y", READ),
             )),
         }))

    show("pending: callee is not part of the finite model",
         Program({"main": Function("main", (), (
             Open("x"),
             Call(None, "external_close", ("x",)),
             Use("x", READ),
         ))}))


if __name__ == "__main__":
    main()
