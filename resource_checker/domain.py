"""Explicit abstract domain for resource ownership.

The abstract domain tracks, per resource, an explicit ownership state --
there is deliberately **no global reference count**: ownership transfer is
modelled by moving the ``owner`` field and marking the source variable as
``MOVED``, so a previous owner can no longer release the resource.

Per-resource state:

- ``owned``           : definitely owned by ``owner`` (a variable)
- ``released``        : definitely released
- ``maybe_released``  : weak update through a may-alias set; may or may not
                        have been released (imprecision to be resolved by
                        concrete replay)

Each variable maps to a set of tokens (points-to set).  A token is a
resource id, ``NULL`` or ``MOVED``.  Singleton sets give strong (precise)
updates; larger sets give weak updates and ``maybe_*`` imprecision.

Join points use an explicit powerset domain: the analysis keeps a concrete
*set* of abstract states per program point and joins by set union.
"""
from __future__ import annotations

from dataclasses import dataclass, field

NULL = "NULL"
MOVED = "MOVED"

OWNED = "owned"
RELEASED = "released"
MAYBE_RELEASED = "maybe_released"

RESOURCE_STATES = frozenset({OWNED, RELEASED, MAYBE_RELEASED})


@dataclass(frozen=True)
class ResInfo:
    state: str = OWNED
    owner: str | None = None
    borrows: frozenset[str] = frozenset()

    def __post_init__(self) -> None:
        if self.state not in RESOURCE_STATES:
            raise ValueError(f"unknown resource state: {self.state!r}")


class AbsState:
    """One abstract state: variable environment + per-resource info."""

    __slots__ = ("env", "res", "_key")

    def __init__(
        self,
        env: dict[str, frozenset[str]] | None = None,
        res: dict[str, ResInfo] | None = None,
    ) -> None:
        self.env: dict[str, frozenset[str]] = {
            var: frozenset(tokens) for var, tokens in (env or {}).items()
        }
        self.res: dict[str, ResInfo] = dict(res or {})
        self._key: tuple | None = None

    # -- structural equality / hashing (needed for the explicit state set) --
    def key(self) -> tuple:
        if self._key is None:
            env_key = tuple(
                sorted((var, tuple(sorted(tokens))) for var, tokens in self.env.items())
            )
            res_key = tuple(
                sorted(
                    (rid, info.state, info.owner or "", tuple(sorted(info.borrows)))
                    for rid, info in self.res.items()
                )
            )
            self._key = (env_key, res_key)
        return self._key

    def __hash__(self) -> int:
        return hash(self.key())

    def __eq__(self, other: object) -> bool:
        return isinstance(other, AbsState) and self.key() == other.key()

    def copy(self) -> "AbsState":
        return AbsState(self.env, self.res)

    # -- accessors ----------------------------------------------------------
    def lookup(self, var: str) -> frozenset[str]:
        """Points-to token set of ``var`` (unbound variables are NULL)."""
        return self.env.get(var, frozenset({NULL}))

    def with_env(self, var: str, tokens) -> "AbsState":
        new = self.copy()
        new.env[var] = frozenset(tokens)
        return new

    def with_res(self, rid: str, info: ResInfo) -> "AbsState":
        new = self.copy()
        new.res[rid] = info
        return new

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        env = {k: sorted(v) for k, v in self.env.items()}
        res = {k: (v.state, v.owner, sorted(v.borrows)) for k, v in self.res.items()}
        return f"AbsState(env={env}, res={res})"


@dataclass
class StateSpace:
    """Explicit powerset domain: program point -> set of abstract states.

    Join at merge points is plain set union; no widening is applied, which
    is sound here because the verification object is a finite-state model.
    """

    states: dict[str, set[AbsState]] = field(default_factory=dict)

    def join(self, node: str, state: AbsState) -> bool:
        """Add ``state`` at ``node``; return True if it was new."""
        bucket = self.states.setdefault(node, set())
        if state in bucket:
            return False
        bucket.add(state)
        return True

    def total(self) -> int:
        return sum(len(bucket) for bucket in self.states.values())
