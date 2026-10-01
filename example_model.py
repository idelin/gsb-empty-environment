"""Example finite-state model: a primary/replica register with fault injection.

State variables (defaults in ``DEFAULTS``):
    primary_up  : is the primary alive
    primary     : value held by the primary
    replica     : value held by the replica
    acked       : a write has been acknowledged to the client
    replicated  : a replication round has reported success
    last_read   : value observed by the last client read (None if none)
    audited     : an audit round has run
    cache_warm  : irrelevant bookkeeping flag (shrinkable noise)
    debug       : irrelevant bookkeeping flag (shrinkable noise)

Faults (members of ``FAULT_UNIVERSE``):
    partition    : replication reports success but silently drops the write
    double_apply : replication applies the delta twice (replica overshoots)
    logging      : no-op fault (noise)
    metrics      : no-op fault (noise)

Error signatures:
    ERR_STALE_READ    : an acknowledged, "successfully replicated" write is
                        not visible to a later read
    ERR_REPLICA_AHEAD : an audit finds the replica ahead of the primary
"""
from __future__ import annotations

from model import Action, Model

MAX_WRITE = 2  # keeps the model finite

ERR_STALE_READ = "ERR_STALE_READ"
ERR_REPLICA_AHEAD = "ERR_REPLICA_AHEAD"

DEFAULTS = {
    "primary_up": False,
    "primary": 0,
    "replica": 0,
    "acked": False,
    "replicated": False,
    "last_read": None,
    "audited": False,
    "cache_warm": False,
    "debug": False,
}

FAULT_UNIVERSE = frozenset({"partition", "double_apply", "logging", "metrics"})


def _eff_write(state, faults):
    state["primary"] += 1
    state["acked"] = True
    state["replicated"] = False
    state["last_read"] = None


def _eff_replicate(state, faults):
    if "partition" in faults:
        state["replicated"] = True  # claims success, silently drops the write
    elif "double_apply" in faults:
        state["replica"] += 2 * (state["primary"] - state["replica"])
        state["replicated"] = True
    else:
        state["replica"] = state["primary"]
        state["replicated"] = True
    # A new replication round invalidates earlier reads: only a read that
    # happens *after* a "successful" replication can be a stale read.
    state["last_read"] = None


def _error_of(state, faults):
    if (state["acked"] and state["replicated"]
            and state["last_read"] is not None
            and state["last_read"] != state["primary"]):
        return ERR_STALE_READ
    if state["audited"] and state["replica"] > state["primary"]:
        return ERR_REPLICA_AHEAD
    return None


def build_model() -> Model:
    actions = {a.name: a for a in [
        Action("start",
               lambda s, f: not s["primary_up"],
               lambda s, f: s.__setitem__("primary_up", True)),
        Action("write",
               lambda s, f: s["primary_up"] and s["primary"] < MAX_WRITE,
               _eff_write),
        Action("replicate",
               lambda s, f: s["primary_up"],
               _eff_replicate),
        Action("read",
               lambda s, f: True,
               lambda s, f: s.__setitem__("last_read", s["replica"])),
        Action("audit",
               lambda s, f: True,
               lambda s, f: s.__setitem__("audited", True)),
        Action("crash",
               lambda s, f: s["primary_up"],
               lambda s, f: s.__setitem__("primary_up", False)),
        Action("noop",
               lambda s, f: True,
               lambda s, f: None),
    ]}
    return Model("replicated-register", dict(DEFAULTS), actions, _error_of)
