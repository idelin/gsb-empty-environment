"""Example finite-state models used by the demo runner and the tests."""

from __future__ import annotations

from porcheck.model import Action, Invariant, Model


def independent_counters(n: int = 3, m: int = 4, deadlock_is_error: bool = False) -> Model:
    """Two processes each increment their own counter: fully independent.

    Full enumeration explores (n+1)*(m+1) states; the reduced search only
    n+m+1, because one counter's increment commutes with the other's.
    The state (n, m) is a deadlock (normal termination).
    """
    inc_x = Action(
        "inc_x", frozenset({"x"}), frozenset({"x"}),
        lambda d: d["x"] < n,
        lambda d: {**d, "x": d["x"] + 1},
    )
    inc_y = Action(
        "inc_y", frozenset({"y"}), frozenset({"y"}),
        lambda d: d["y"] < m,
        lambda d: {**d, "y": d["y"] + 1},
    )
    return Model(
        name=f"independent_counters({n},{m})",
        initial={"x": 0, "y": 0},
        actions=(inc_x, inc_y),
        deadlock_is_error=deadlock_is_error,
    )


def shared_counter(k: int = 2) -> Model:
    """Two processes increment one shared counter: write-write conflict.

    Both actions write `c`, so they are never independent and the reduced
    search must coincide with full enumeration.
    """
    p_inc = Action(
        "p_inc", frozenset({"c"}), frozenset({"c"}),
        lambda d: d["c"] < k,
        lambda d: {**d, "c": d["c"] + 1},
    )
    q_inc = Action(
        "q_inc", frozenset({"c"}), frozenset({"c"}),
        lambda d: d["c"] < k,
        lambda d: {**d, "c": d["c"] + 1},
    )
    bounded = Invariant(
        "c_bounded", frozenset({"c"}), lambda d: d["c"] <= k
    )
    return Model(
        name=f"shared_counter({k})",
        initial={"c": 0},
        actions=(p_inc, q_inc),
        invariants=(bounded,),
        deadlock_is_error=False,
    )


def copy_violation(bound: int = 2) -> Model:
    """`copy` duplicates x into y; invariant y <= 1 breaks once x == 2.

    `bump` writes x, which `copy` reads, so the two are dependent and never
    reduced.  `tick` only touches its own variable z: it is invisible to the
    property and independent, so the reduced search skips its interleavings
    yet still finds the violation.
    """
    bump = Action(
        "bump", frozenset({"x"}), frozenset({"x"}),
        lambda d: d["x"] < bound,
        lambda d: {**d, "x": d["x"] + 1},
    )
    copy = Action(
        "copy", frozenset({"x", "y"}), frozenset({"y"}),
        lambda d: d["y"] != d["x"],
        lambda d: {**d, "y": d["x"]},
    )
    tick = Action(
        "tick", frozenset({"z"}), frozenset({"z"}),
        lambda d: d["z"] < 2,
        lambda d: {**d, "z": d["z"] + 1},
    )
    small_y = Invariant("y_small", frozenset({"y"}), lambda d: d["y"] <= 1)
    return Model(
        name=f"copy_violation({bound})",
        initial={"x": 0, "y": 0, "z": 0},
        actions=(bump, copy, tick),
        invariants=(small_y,),
    )


def lock_deadlock() -> Model:
    """Two threads take locks A and B in opposite order: classic deadlock.

    p1 takes A then B; p2 takes B then A.  Each critical section is atomic
    and the threads loop forever, so the only deadlock is the lock-ordering
    one: p1 holds A and wants B while p2 holds B and wants A.
    """
    p1_take_a = Action(
        "p1_take_a", frozenset({"la", "pc1"}), frozenset({"la", "pc1"}),
        lambda d: d["pc1"] == 0 and d["la"] == 0,
        lambda d: {**d, "la": 1, "pc1": 1},
    )
    p1_take_b = Action(
        "p1_take_b", frozenset({"lb", "pc1"}), frozenset({"la", "pc1"}),
        lambda d: d["pc1"] == 1 and d["lb"] == 0,
        lambda d: {**d, "la": 0, "pc1": 0},
    )
    p2_take_b = Action(
        "p2_take_b", frozenset({"lb", "pc2"}), frozenset({"lb", "pc2"}),
        lambda d: d["pc2"] == 0 and d["lb"] == 0,
        lambda d: {**d, "lb": 2, "pc2": 1},
    )
    p2_take_a = Action(
        "p2_take_a", frozenset({"la", "pc2"}), frozenset({"lb", "pc2"}),
        lambda d: d["pc2"] == 1 and d["la"] == 0,
        lambda d: {**d, "lb": 0, "pc2": 0},
    )
    return Model(
        name="lock_deadlock",
        initial={"la": 0, "lb": 0, "pc1": 0, "pc2": 0},
        actions=(p1_take_a, p1_take_b, p2_take_b, p2_take_a),
        deadlock_is_error=True,
    )


def cycle_ignoring() -> Model:
    """Demonstrates why the cycle proviso is needed.

    `flip` toggles x forever and is independent of `set_y`; without the
    cycle proviso the reduced search keeps choosing {flip} and `set_y` is
    ignored on the cycle, so the invariant violation y == 1 is missed.
    """
    flip = Action(
        "flip", frozenset({"x"}), frozenset({"x"}),
        lambda d: True,
        lambda d: {**d, "x": 1 - d["x"]},
    )
    set_y = Action(
        "set_y", frozenset({"y"}), frozenset({"y"}),
        lambda d: d["y"] == 0,
        lambda d: {**d, "y": 1},
    )
    y_zero = Invariant("y_zero", frozenset({"y"}), lambda d: d["y"] == 0)
    return Model(
        name="cycle_ignoring",
        initial={"x": 0, "y": 0},
        actions=(flip, set_y),
        invariants=(y_zero,),
        deadlock_is_error=False,
    )
