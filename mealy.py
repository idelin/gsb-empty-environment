"""Deterministic Mealy machine model: loading, validation, reachability.

Python 3.11 standard library only.

Model JSON schema
-----------------
{
  "name": "optional string",
  "states": ["s0", "s1", ...],
  "inputs": ["a", "b", ...],
  "outputs": ["0", "1", ...],
  "transitions": {                 -- may be partial: an input may be
      "s0": {"a": ["s1", "0"]},       undefined at some states
      ...
  },
  "initial_candidates": ["s0", "s1"],   -- the actual initial state is one of
                                        -- these; the tester cannot tell which
  "reset": {                         -- optional; omit to disable reset
      "target": "s0",                -- reset always lands in this state
      "cost": 3,                     -- positive integer
      "allowed_states": ["s0", "s1"] -- reset is only legal in these states
  },
  "input_costs": {"a": 1, "b": 2}    -- positive integer cost per input
}

Semantics
---------
* The tester only observes the output produced after each input.  Internal
  states are never directly observable.
* An input is *safe* at a belief (set of candidate states) iff it is defined
  for every candidate state.
* Reset is *safe* iff every candidate state is in ``reset.allowed_states``;
  it moves the machine to ``reset.target`` and produces no observation.
* ``all_transitions`` is the set of (state, input) pairs whose state is
  reachable from the initial candidates (via transitions and reset edges).
  These are exactly the transitions a test plan must definitely cover.
"""
from __future__ import annotations

import json
from dataclasses import dataclass


class ModelError(ValueError):
    """Raised when a model JSON document is invalid."""


def _require(cond: bool, msg: str) -> None:
    if not cond:
        raise ModelError(msg)


@dataclass(frozen=True)
class Model:
    name: str
    states: tuple[str, ...]
    inputs: tuple[str, ...]
    outputs: tuple[str, ...]
    # (state, input) -> (next_state, output)
    trans: dict[tuple[str, str], tuple[str, str]]
    initial_candidates: tuple[str, ...]
    reset_target: str | None
    reset_cost: int
    reset_allowed: frozenset[str]
    input_costs: dict[str, int]
    # transitions (state, input) reachable from the initial candidates
    all_transitions: frozenset[tuple[str, str]]
    min_input_cost: int

    # ------------------------------------------------------------------ api
    def next(self, state: str, symbol: str) -> tuple[str, str] | None:
        return self.trans.get((state, symbol))

    def has_reset(self) -> bool:
        return self.reset_target is not None

    # ------------------------------------------------------------- loading
    @staticmethod
    def from_json_file(path: str) -> "Model":
        with open(path, "r", encoding="utf-8") as fh:
            doc = json.load(fh)
        return Model.from_dict(doc)

    @staticmethod
    def from_dict(doc: dict) -> "Model":
        _require(isinstance(doc, dict), "model must be a JSON object")
        name = str(doc.get("name", "<unnamed>"))

        states = _str_list(doc, "states")
        inputs = _str_list(doc, "inputs")
        outputs = _str_list(doc, "outputs")

        raw_trans = doc.get("transitions")
        _require(isinstance(raw_trans, dict), "'transitions' must be an object")
        trans: dict[tuple[str, str], tuple[str, str]] = {}
        for st, per_input in raw_trans.items():
            _require(st in states, f"transition from unknown state {st!r}")
            _require(isinstance(per_input, dict),
                     f"transitions[{st!r}] must be an object")
            for sym, dst_out in per_input.items():
                _require(sym in inputs,
                         f"transition ({st},{sym}): unknown input")
                _require(isinstance(dst_out, list) and len(dst_out) == 2,
                         f"transition ({st},{sym}) must be [next, output]")
                nxt, out = dst_out
                _require(nxt in states,
                         f"transition ({st},{sym}): unknown next state")
                _require(out in outputs,
                         f"transition ({st},{sym}): unknown output")
                trans[(st, sym)] = (nxt, out)

        cands = doc.get("initial_candidates")
        _require(isinstance(cands, list) and cands,
                 "'initial_candidates' must be a non-empty list")
        for c in cands:
            _require(c in states, f"initial candidate {c!r} not a state")
        initial_candidates = tuple(dict.fromkeys(cands))

        reset_doc = doc.get("reset")
        if reset_doc is None:
            reset_target, reset_cost, reset_allowed = None, 0, frozenset()
        else:
            _require(isinstance(reset_doc, dict), "'reset' must be an object")
            reset_target = reset_doc.get("target")
            _require(reset_target in states, "reset target must be a state")
            reset_cost = reset_doc.get("cost")
            _require(isinstance(reset_cost, int) and reset_cost > 0,
                     "reset cost must be a positive integer")
            allowed = reset_doc.get("allowed_states")
            _require(isinstance(allowed, list),
                     "reset.allowed_states must be a list")
            for s in allowed:
                _require(s in states, f"reset allowed state {s!r} unknown")
            reset_allowed = frozenset(allowed)

        raw_costs = doc.get("input_costs")
        _require(isinstance(raw_costs, dict), "'input_costs' must be an object")
        input_costs: dict[str, int] = {}
        for sym in inputs:
            c = raw_costs.get(sym)
            _require(isinstance(c, int) and c > 0,
                     f"input cost for {sym!r} must be a positive integer")
            input_costs[sym] = c

        model = Model(
            name=name,
            states=tuple(states),
            inputs=tuple(inputs),
            outputs=tuple(outputs),
            trans=trans,
            initial_candidates=initial_candidates,
            reset_target=reset_target,
            reset_cost=reset_cost,
            reset_allowed=reset_allowed,
            input_costs=input_costs,
            all_transitions=frozenset(),
            min_input_cost=min(input_costs.values()),
        )
        object.__setattr__(model, "all_transitions",
                           frozenset(_reachable_transitions(model)))
        return model


def _str_list(doc: dict, key: str) -> tuple[str, ...]:
    val = doc.get(key)
    _require(isinstance(val, list) and val, f"'{key}' must be a non-empty list")
    for x in val:
        _require(isinstance(x, str), f"'{key}' entries must be strings")
    _require(len(set(val)) == len(val), f"'{key}' entries must be unique")
    return tuple(val)


def _reachable_transitions(m: Model) -> set[tuple[str, str]]:
    """Transitions whose source state is reachable from the initial
    candidates via transition edges and reset edges."""
    adj: dict[str, set[str]] = {s: set() for s in m.states}
    for (s, _i), (ns, _o) in m.trans.items():
        adj[s].add(ns)
    if m.reset_target is not None:
        for s in m.reset_allowed:
            adj[s].add(m.reset_target)
    seen: set[str] = set()
    stack = list(m.initial_candidates)
    while stack:
        x = stack.pop()
        if x in seen:
            continue
        seen.add(x)
        stack.extend(adj[x])
    return {(s, i) for (s, i) in m.trans if s in seen}
