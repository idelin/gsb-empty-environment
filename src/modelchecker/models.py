"""Registry of example finite-state models plus a JSON loader."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Mapping

from .model import FiniteModel


def _chain(n: int) -> dict[int, list[int]]:
    return {i: [i + 1] for i in range(n - 1)}


def two_bit_counter() -> FiniteModel:
    return FiniteModel.build(
        name="two_bit_counter",
        states=[0, 1, 2, 3],
        initial=[0],
        transitions={0: [1], 1: [2], 2: [3], 3: [0]},
        bad_states=[],
    )


def trap_unsafe() -> FiniteModel:
    return FiniteModel.build(
        name="trap_unsafe",
        states=[0, 1, 2, 3],
        initial=[0],
        transitions={0: [1], 1: [2], 2: [2], 3: [2]},
        bad_states=[2],
    )


def unreachable_bad() -> FiniteModel:
    return FiniteModel.build(
        name="unreachable_bad",
        states=[0, 1, 2],
        initial=[0],
        transitions={0: [1], 1: [1], 2: [1]},
        bad_states=[2],
    )


def long_chain() -> FiniteModel:
    return FiniteModel.build(
        name="long_chain",
        states=list(range(10)),
        initial=[0],
        transitions=_chain(10),
        bad_states=[],
    )


MODEL_FACTORIES = {
    "two_bit_counter": two_bit_counter,
    "trap_unsafe": trap_unsafe,
    "unreachable_bad": unreachable_bad,
    "long_chain": long_chain,
}


def load_from_json(data: Mapping[str, object]) -> FiniteModel:
    """Build a model from a JSON description.

    Transition-table keys are JSON encodings of states, matching the format
    used in certificates.
    """
    name = data.get("name", "custom")
    states = data["states"]
    initial = data["initial"]
    raw_transitions = data.get("transitions", {})
    transitions = {
        json.loads(key): targets for key, targets in raw_transitions.items()
    }
    bad_states = data.get("bad_states", [])
    return FiniteModel.build(name, states, initial, transitions, bad_states)


def load_model(name_or_path: str) -> FiniteModel:
    if name_or_path in MODEL_FACTORIES:
        return MODEL_FACTORIES[name_or_path]()
    path = Path(name_or_path)
    with path.open("r", encoding="utf-8") as handle:
        data = json.load(handle)
    return load_from_json(data)


def list_models() -> list[str]:
    return sorted(MODEL_FACTORIES)
