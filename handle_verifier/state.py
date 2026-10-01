"""Abstract configurations: alias map plus per-resource state.

A configuration captures one possible execution prefix:

- ``env``:       variable -> resource id (aliases share the same id, so two
                 aliases are never mistaken for two independent resources)
- ``rstate``:    resource id -> open / closed / moved
- ``borrows``:   resource id -> (shared count, exclusive count)
- ``borrow_of``: borrow-handle variable -> (resource id, mode)

Configurations are canonicalised (unreachable resources dropped, ids
renumbered deterministically) so the reachable space is finite and set
based fixpoints terminate.
"""
from __future__ import annotations

import enum
from dataclasses import dataclass


class RState(enum.Enum):
    OPEN = "open"
    CLOSED = "closed"
    MOVED = "moved"


@dataclass(frozen=True)
class Config:
    env: tuple        # ((var, rid), ...) sorted by var
    rstate: tuple     # ((rid, RState), ...) sorted by rid
    borrows: tuple    # ((rid, (shared, exclusive)), ...) sorted by rid
    borrow_of: tuple  # ((var, (rid, mode)), ...) sorted by var


def _note(rid, order):
    if rid not in order:
        order.append(rid)


def from_maps(env=None, rstate=None, borrows=None, borrow_of=None):
    """Build a canonical Config from plain dicts."""
    env = dict(env or {})
    rstate = dict(rstate or {})
    borrows = {k: tuple(v) for k, v in (borrows or {}).items()}
    borrow_of = {k: tuple(v) for k, v in (borrow_of or {}).items()}
    order = []
    for var in sorted(env):
        _note(env[var], order)
    for var in sorted(borrow_of):
        _note(borrow_of[var][0], order)
    remap = {old: new for new, old in enumerate(order)}
    new_env = {v: remap[r] for v, r in env.items()}
    new_borrow_of = {v: (remap[r], m) for v, (r, m) in borrow_of.items()}
    new_rstate = {}
    new_borrows = {}
    for old, new in remap.items():
        new_rstate[new] = rstate.get(old, RState.OPEN)
        new_borrows[new] = borrows.get(old, (0, 0))
    return Config(
        env=tuple(sorted(new_env.items())),
        rstate=tuple(sorted(new_rstate.items())),
        borrows=tuple(sorted(new_borrows.items())),
        borrow_of=tuple(sorted(new_borrow_of.items())),
    )


def to_maps(config):
    """Unpack a Config into mutable dicts (env, rstate, borrows, borrow_of)."""
    return (
        dict(config.env),
        dict(config.rstate),
        {k: tuple(v) for k, v in config.borrows},
        {k: tuple(v) for k, v in config.borrow_of},
    )


def env_get(config, var):
    return dict(config.env).get(var)


def state_of(config, rid):
    return dict(config.rstate).get(rid)


def borrow_counts(config, rid):
    return dict(config.borrows).get(rid, (0, 0))


def borrow_handle(config, var):
    return dict(config.borrow_of).get(var)
