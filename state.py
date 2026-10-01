"""Abstract states for the handle verifier.

A resource identity is a pair ``(site, k)``: the syntactic Open site plus a
small instance index (k-limited widening keeps the domain finite).  Aliases
are variables whose env entry contains the *same* identity, so two aliases
can never be mistaken for two independent resources.

Because control-flow joins are conservative, every component is a *may* set:
a resource's status is the set of statuses it may have, ``env[v]`` is the set
of identities ``v`` may hold, and ``moved`` is the set of variables that may
have been moved from.
"""
from __future__ import annotations

from dataclasses import dataclass

OPEN = "open"
CLOSED = "closed"


@dataclass(frozen=True)
class ResourceInfo:
    statuses: frozenset               # subset of {OPEN, CLOSED}
    shared: frozenset = frozenset()   # variables holding a shared borrow
    mut: frozenset = frozenset()      # variables possibly holding a mut borrow

    def join(self, other):
        return ResourceInfo(self.statuses | other.statuses,
                            self.shared | other.shared,
                            self.mut | other.mut)

    def leq(self, other):
        return (self.statuses <= other.statuses
                and self.shared <= other.shared
                and self.mut <= other.mut)


class AState:
    __slots__ = ("env", "res", "moved")

    def __init__(self, env=None, res=None, moved=frozenset()):
        self.env = env if env is not None else {}      # var -> frozenset[rid]
        self.res = res if res is not None else {}      # rid -> ResourceInfo
        self.moved = moved                             # frozenset[var]

    def copy(self):
        return AState(dict(self.env), dict(self.res), self.moved)

    def key(self):
        env_k = tuple(sorted((v, tuple(sorted(ids))) for v, ids in self.env.items()))
        res_k = tuple(sorted(
            (rid, (tuple(sorted(i.statuses)), tuple(sorted(i.shared)),
                   tuple(sorted(i.mut))))
            for rid, i in self.res.items()))
        return (env_k, res_k, tuple(sorted(self.moved)))

    def leq(self, other):
        for v, ids in self.env.items():
            if not ids <= other.env.get(v, frozenset()):
                return False
        for rid, info in self.res.items():
            oinfo = other.res.get(rid)
            if oinfo is None or not info.leq(oinfo):
                return False
        return self.moved <= other.moved

    def join(self, other):
        env = {}
        for v in self.env.keys() | other.env.keys():
            ids = self.env.get(v, frozenset()) | other.env.get(v, frozenset())
            if ids:
                env[v] = ids
        res = {}
        for rid in self.res.keys() | other.res.keys():
            a, b = self.res.get(rid), other.res.get(rid)
            res[rid] = a.join(b) if a and b else (a or b)
        return AState(env, res, self.moved | other.moved)

    def gc(self, extra=frozenset()):
        """Drop resource identities no variable (or returned value) can name."""
        reach = set(extra)
        for ids in self.env.values():
            reach |= set(ids)
        return AState(dict(self.env),
                      {r: i for r, i in self.res.items() if r in reach},
                      self.moved)

    def pretty(self):
        def rid_s(rid):
            return "r%d_%d" % rid
        env = ", ".join("%s->{%s}" % (v, ",".join(sorted(rid_s(r) for r in ids)))
                        for v, ids in sorted(self.env.items())) or "-"
        res = ", ".join("%s:%s sh=%s mut=%s"
                        % (rid_s(r), "/".join(sorted(i.statuses)),
                           sorted(i.shared) or "-", sorted(i.mut) or "-")
                        for r, i in sorted(self.res.items())) or "-"
        return "env[%s] res[%s] moved=%s" % (env, res, sorted(self.moved))
