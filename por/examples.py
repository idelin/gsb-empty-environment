"""Sample finite-state models used by the demo and the tests."""
from __future__ import annotations

from .model import Action, Model, make_state, state_dict


def _set(state, **updates):
    data = state_dict(state)
    data.update(updates)
    return make_state(data)


def independent_counters(n=2, k=3) -> Model:
    """n processes each increment a private counter 0..k (fully independent)."""
    variables = tuple(f"c{i}" for i in range(n))
    actions = []
    for i, var in enumerate(variables):
        actions.append(
            Action(
                name=f"inc_{i}",
                guard=lambda s, v=var: state_dict(s)[v] < k,
                effect=lambda s, v=var: _set(s, **{v: state_dict(s)[v] + 1}),
                reads=frozenset({var}),
                writes=frozenset({var}),
            )
        )
    return Model(
        name=f"independent_counters(n={n},k={k})",
        variables=variables,
        initial=make_state({v: 0 for v in variables}),
        actions=tuple(actions),
        check_deadlock=False,  # termination at c_i == k is expected
    )


def counters_with_flag(n=2, k=3) -> Model:
    """Independent counters plus a property on a variable nobody writes."""
    base = independent_counters(n, k)
    variables = base.variables + ("z",)
    initial = make_state(dict(state_dict(base.initial), z=0))
    return Model(
        name=f"counters_with_flag(n={n},k={k})",
        variables=variables,
        initial=initial,
        actions=base.actions,
        property_fn=lambda s: state_dict(s)["z"] == 0,
        property_vars=frozenset({"z"}),
        property_name="z stays 0",
        check_deadlock=False,
    )


def shared_counter(n=2, k=4) -> Model:
    """n processes increment one shared counter: nothing is independent."""
    limit = n * k
    actions = tuple(
        Action(
            name=f"inc_{i}",
            guard=lambda s: state_dict(s)["x"] < limit,
            effect=lambda s: _set(s, x=state_dict(s)["x"] + 1),
            reads=frozenset({"x"}),
            writes=frozenset({"x"}),
        )
        for i in range(n)
    )
    return Model(
        name=f"shared_counter(n={n},k={k})",
        variables=("x",),
        initial=make_state({"x": 0}),
        actions=actions,
        check_deadlock=False,
    )


def flag_race() -> Model:
    """Ignoring-problem demo: a self-loop must not hide the flag setter.

    'toggle' spins on x forever; 'set_y' eventually violates the property.
    Without the cycle proviso the reduced search loops on toggle and misses
    the violation.
    """
    toggle = Action(
        name="toggle",
        guard=lambda s: True,
        effect=lambda s: _set(s, x=1 - state_dict(s)["x"]),
        reads=frozenset({"x"}),
        writes=frozenset({"x"}),
    )
    set_y = Action(
        name="set_y",
        guard=lambda s: state_dict(s)["y"] == 0,
        effect=lambda s: _set(s, y=1),
        reads=frozenset({"y"}),
        writes=frozenset({"y"}),
    )
    return Model(
        name="flag_race",
        variables=("x", "y"),
        initial=make_state({"x": 0, "y": 0}),
        actions=(toggle, set_y),
        property_fn=lambda s: state_dict(s)["y"] == 0,
        property_vars=frozenset({"y"}),
        property_name="y stays 0",
        check_deadlock=False,
    )


def deadlock_model() -> Model:
    """Independent actions lead to a state with nothing enabled."""
    inc = Action(
        name="inc",
        guard=lambda s: state_dict(s)["c"] < 2,
        effect=lambda s: _set(s, c=state_dict(s)["c"] + 1),
        reads=frozenset({"c"}),
        writes=frozenset({"c"}),
    )
    setx = Action(
        name="setx",
        guard=lambda s: state_dict(s)["x"] == 0,
        effect=lambda s: _set(s, x=1),
        reads=frozenset({"x"}),
        writes=frozenset({"x"}),
    )
    return Model(
        name="deadlock_model",
        variables=("c", "x"),
        initial=make_state({"c": 0, "x": 0}),
        actions=(inc, setx),
        check_deadlock=True,  # state (c=2, x=1) has no enabled action
    )


def visible_property_counters(n=2, k=2) -> Model:
    """Property reads the counters, so every action is visible: no reduction."""
    base = independent_counters(n, k)
    limit = n * k
    return Model(
        name=f"visible_property_counters(n={n},k={k})",
        variables=base.variables,
        initial=base.initial,
        actions=base.actions,
        property_fn=lambda s: sum(state_dict(s)[v] for v in base.variables) <= limit,
        property_vars=frozenset(base.variables),
        property_name="sum of counters stays within bound",
        check_deadlock=False,
    )


ALL_EXAMPLES = (
    independent_counters,
    counters_with_flag,
    shared_counter,
    flag_race,
    deadlock_model,
    visible_property_counters,
)
