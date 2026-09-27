"""Example finite-state models exercising the checker."""
from __future__ import annotations

from collections import OrderedDict

from . import model as M
from .model import Builder, Edge, Op, Program


def clean_program() -> Program:
    """acquire -> use -> release -> normal exit.  Expected: safe."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.USE, ("x",)), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def leak_on_normal_exit() -> Program:
    """acquire -> use -> normal exit (no release).  Expected: definite leak."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.USE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def exception_with_finally() -> Program:
    """Exception path cleaned up in a finally block.  Expected: safe."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node(
        "n1",
        Op(M.USE, ("x",)),
        [Edge(M.NORMAL, "n2"), Edge(M.FINALLY, "fin")],
        may_raise=True,
    )
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.node("fin", Op(M.RELEASE, ("x",)), [Edge(M.EXCEPTION, "EXC")])
    b.exit("RET", M.EXIT_NORMAL)
    b.exit("EXC", M.EXIT_EXCEPTION)
    return b.build("n0")


def exception_missing_cleanup() -> Program:
    """Finally block forgets to release.  Expected: definite leak at EXC."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node(
        "n1",
        Op(M.USE, ("x",)),
        [Edge(M.NORMAL, "n2"), Edge(M.FINALLY, "fin")],
        may_raise=True,
    )
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.node("fin", Op(M.NOP), [Edge(M.EXCEPTION, "EXC")])
    b.exit("RET", M.EXIT_NORMAL)
    b.exit("EXC", M.EXIT_EXCEPTION)
    return b.build("n0")


def cancel_leak() -> Program:
    """Cancellation edge bypasses the release.  Expected: definite leak at CXL."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node(
        "n1",
        Op(M.USE, ("x",)),
        [Edge(M.NORMAL, "n2"), Edge(M.CANCEL, "CXL")],
        may_cancel=True,
    )
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    b.exit("CXL", M.EXIT_CANCEL)
    return b.build("n0")


def transfer_ok() -> Program:
    """acquire x -> transfer x->y -> release y.  Expected: safe."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.TRANSFER, ("x", "y")), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("y",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def release_after_move() -> Program:
    """Old owner releases after transfer.  Expected: release_after_move."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.TRANSFER, ("x", "y")), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def double_release() -> Program:
    """release -> release.  Expected: double_release (a distinct error)."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def borrow_then_release() -> Program:
    """Release while borrowed.  Expected: release_while_borrowed."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.BORROW, ("x",)), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def borrow_ok() -> Program:
    """borrow -> end_borrow -> release.  Expected: safe."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.BORROW, ("x",)), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.END_BORROW, ("x",)), [Edge(M.NORMAL, "n3")])
    b.node("n3", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def maybe_alias_leak() -> Program:
    """maybe_alias(b, a); release(b): exactly one of the two resources leaks,
    depending on the nondeterministic alias choice.  Expected: possible leaks."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("a",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.ACQUIRE, ("b",)), [Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.MAYBE_ALIAS, ("b", "a")), [Edge(M.NORMAL, "n3")])
    b.node("n3", Op(M.RELEASE, ("b",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


def false_positive_exception() -> Program:
    """Handler exists but the guarded op can never raise (may_raise=False).
    The abstract leak on the exception edge is a false positive that
    concrete replay must filter.  Expected: safe, one filtered candidate."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node(
        "n1",
        Op(M.USE, ("x",)),
        [Edge(M.NORMAL, "n2"), Edge(M.EXCEPTION, "EXC")],
        may_raise=False,
    )
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    b.exit("EXC", M.EXIT_EXCEPTION)
    return b.build("n0")


def loop_overwrite_leak() -> Program:
    """Looping acquire overwrites the previous resource.  Expected: leak."""
    b = Builder()
    b.node("n0", Op(M.ACQUIRE, ("x",)), [Edge(M.NORMAL, "n1")])
    b.node("n1", Op(M.USE, ("x",)), [Edge(M.NORMAL, "n0"), Edge(M.NORMAL, "n2")])
    b.node("n2", Op(M.RELEASE, ("x",)), [Edge(M.NORMAL, "RET")])
    b.exit("RET", M.EXIT_NORMAL)
    return b.build("n0")


EXAMPLES: "OrderedDict[str, tuple]" = OrderedDict(
    [
        ("clean_program", (clean_program, "acquire/use/release, normal exit")),
        ("leak_on_normal_exit", (leak_on_normal_exit, "missing release on normal exit")),
        ("exception_with_finally", (exception_with_finally, "finally cleans up on exception")),
        ("exception_missing_cleanup", (exception_missing_cleanup, "finally forgets to release")),
        ("cancel_leak", (cancel_leak, "cancellation bypasses release")),
        ("transfer_ok", (transfer_ok, "new owner releases after transfer")),
        ("release_after_move", (release_after_move, "old owner releases after transfer")),
        ("double_release", (double_release, "resource released twice")),
        ("borrow_then_release", (borrow_then_release, "release while borrowed")),
        ("borrow_ok", (borrow_ok, "borrow ended before release")),
        ("maybe_alias_leak", (maybe_alias_leak, "nondeterministic alias, conditional leak")),
        ("false_positive_exception", (false_positive_exception, "never-raising op with handler")),
        ("loop_overwrite_leak", (loop_overwrite_leak, "looping acquire overwrites live resource")),
    ]
)

#: acyclic examples used for the exhaustive ground-truth cross-check
CROSS_CHECKED = [
    "clean_program",
    "leak_on_normal_exit",
    "exception_with_finally",
    "exception_missing_cleanup",
    "cancel_leak",
    "transfer_ok",
    "release_after_move",
    "double_release",
    "borrow_then_release",
    "borrow_ok",
    "maybe_alias_leak",
    "false_positive_exception",
]
