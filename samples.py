"""Sample finite-state models exercising the checker."""

from model import (
    CANCEL,
    EXCEPT,
    FINALLY,
    Acquire,
    Alias,
    Borrow,
    Edge,
    Node,
    Nop,
    Program,
    Release,
    Set,
    Transfer,
)


def ok_with_finally() -> Program:
    """Correct program: moved to the logger on the normal path, released in
    the finally-cleanup on the exceptional path.  Includes a retry loop."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("work")]),
        Node("work", Nop(), [
            Edge("work"),                 # retry loop
            Edge("handoff"),              # normal completion
            Edge("cleanup", EXCEPT),      # failure -> finally cleanup
        ]),
        Node("handoff", Transfer("file", "main", "logger"), [Edge("store")]),
        Node("store", Release("file", "logger"), [Edge("exit_normal")]),
        Node("cleanup", Release("file", "main"), [Edge("exit_exc", FINALLY)]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
        Node("exit_exc", Nop(), [], exit_kind="exception"),
    ]
    return Program("ok_with_finally", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main", "logger"))


def cancel_leak() -> Program:
    """The cancellation edge skips cleanup: definite leak at the cancel exit."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("work")]),
        Node("work", Nop(), [
            Edge("finish"),
            Edge("cleanup", EXCEPT),
            Edge("exit_cancel", CANCEL),  # cancellation skips the release
        ]),
        Node("finish", Release("file", "main"), [Edge("exit_normal")]),
        Node("cleanup", Release("file", "main"), [Edge("exit_exc", FINALLY)]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
        Node("exit_exc", Nop(), [], exit_kind="exception"),
        Node("exit_cancel", Nop(), [], exit_kind="cancel"),
    ]
    return Program("cancel_leak", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main",))


def double_release() -> Program:
    """The normal path releases twice: definite double-release error."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("work")]),
        Node("work", Nop(), [
            Edge("rel1"),
            Edge("cleanup", EXCEPT),
        ]),
        Node("rel1", Release("file", "main"), [Edge("rel2")]),
        Node("rel2", Release("file", "main"), [Edge("exit_normal")]),
        Node("cleanup", Release("file", "main"), [Edge("exit_exc", FINALLY)]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
        Node("exit_exc", Nop(), [], exit_kind="exception"),
    ]
    return Program("double_release", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main",))


def moved_release() -> Program:
    """After the move, the old owner must not release: definite
    release-after-move error."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("handoff")]),
        Node("handoff", Transfer("file", "main", "worker"), [Edge("rel")]),
        Node("rel", Release("file", "main"), [Edge("exit_normal")]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
    ]
    return Program("moved_release", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main", "worker"))


def borrow_release() -> Program:
    """Releasing while a borrow is active: definite borrow violation."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("borrow")]),
        Node("borrow", Borrow("file", "main", "reader"), [Edge("rel")]),
        Node("rel", Release("file", "main"), [Edge("exit_normal")]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
    ]
    return Program("borrow_release", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main", "reader"))


def alias_ok() -> Program:
    """An alias may release on behalf of the owner; aliases never own."""
    nodes = [
        Node("entry", Nop(), [Edge("acquire")]),
        Node("acquire", Acquire("file", "main"), [Edge("mkalias")]),
        Node("mkalias", Alias("file", "f"), [Edge("rel")]),
        Node("rel", Release("file", "f"), [Edge("exit_normal")]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
    ]
    return Program("alias_ok", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main",))


def guarded_false_positive() -> Program:
    """Correlated guards: acquire and release happen under the same flag.
    The path-insensitive abstract analysis reports a possible leak and a
    possible release-without-acquire; exhaustive concrete replay refutes
    both (filtered false positives)."""
    nodes = [
        Node("entry", Set("ok", True), [Edge("branch1")]),
        Node("branch1", Nop(), [
            Edge("acquire", guard=("ok", True)),
            Edge("join1", guard=("ok", False)),
        ]),
        Node("acquire", Acquire("file", "main"), [Edge("join1")]),
        Node("join1", Nop(), [Edge("branch2")]),
        Node("branch2", Nop(), [
            Edge("rel", guard=("ok", True)),
            Edge("exit_normal", guard=("ok", False)),
        ]),
        Node("rel", Release("file", "main"), [Edge("exit_normal")]),
        Node("exit_normal", Nop(), [], exit_kind="normal"),
    ]
    return Program("guarded_false_positive", {n.name: n for n in nodes}, "entry",
                   ("file",), ("main",), vars=("ok",))


ALL = [
    ok_with_finally,
    cancel_leak,
    double_release,
    moved_release,
    borrow_release,
    alias_ok,
    guarded_false_positive,
]
