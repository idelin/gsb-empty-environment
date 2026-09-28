"""Small example models used by the demo and the test suite."""

from __future__ import annotations

from .model import NO_PID, Model, State, Transition

IDLE = "idle"
WAITING = "waiting"
CRITICAL = "critical"


def _set_local(state: State, pid: int, value: str) -> State:
    locals_ = list(state.locals)
    locals_[pid] = value
    return State(shared=state.shared, locals=tuple(locals_))


def at_most_one_critical(state: State) -> bool:
    return sum(1 for local in state.locals if local == CRITICAL) <= 1


def make_mutex_model(num_workers: int = 2, buggy: bool = False) -> Model:
    """One distinguished leader plus ``num_workers`` workers contending for
    a single critical section.

    ``shared[0]`` is the PID of the holder, or :data:`NO_PID`. The leader is
    declared as its own role group and is therefore never permuted with a
    worker. When ``buggy`` is true the acquire transition ignores the holder
    check, allowing two processes into the critical section.
    """
    roles = ("leader",) + ("worker",) * num_workers
    n = len(roles)
    initial = State(shared=(NO_PID,), locals=(IDLE,) * n)

    def request_guard(pid, state):
        return state.locals[pid] == IDLE

    def request_effect(pid, state):
        return _set_local(state, pid, WAITING)

    def acquire_guard(pid, state):
        if state.locals[pid] != WAITING:
            return False
        return buggy or state.shared[0] == NO_PID

    def acquire_effect(pid, state):
        moved = _set_local(state, pid, CRITICAL)
        return State(shared=(pid,), locals=moved.locals)

    def release_guard(pid, state):
        return state.locals[pid] == CRITICAL and state.shared[0] == pid

    def release_effect(pid, state):
        moved = _set_local(state, pid, IDLE)
        return State(shared=(NO_PID,), locals=moved.locals)

    transitions = (
        Transition("request", request_guard, request_effect),
        Transition("acquire", acquire_guard, acquire_effect),
        Transition("release", release_guard, release_effect),
    )
    return Model(roles, initial, transitions, pid_vars=(0,))


def make_hidden_leader_model() -> Model:
    """A model that *claims* all processes are workers but hard-codes a
    special transition for process 0.

    The declared role structure is fully symmetric while the transition
    relation is not, which the symmetry-condition check must detect.
    """
    roles = ("worker", "worker")
    initial = State(shared=(), locals=(IDLE, IDLE))

    def special_guard(pid, state):
        return pid == 0 and state.locals[0] == IDLE

    def special_effect(pid, state):
        return _set_local(state, pid, WAITING)

    transitions = (
        Transition("special_request", special_guard, special_effect),
    )
    return Model(roles, initial, transitions)
