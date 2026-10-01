"""Machine states and sharing-preserving normalization.

A state is an immutable snapshot: an explicit control stack (frames), an
environment mapping variables to values, and a finite heap of arrays.
Values are plain ints or `Ref` heap references.

`normalize` canonicalizes heap references by renaming object ids in a
deterministic reachability order starting from the environment.  This is a
graph renaming, NOT a sort of values: two variables aliasing the same object
keep pointing to the same (renamed) id, while two variables pointing to
different objects with equal contents keep different ids.  Sharing identity
is therefore preserved and states are deduplicated up to object naming.
"""

from dataclasses import dataclass, replace

from .faults import Fault  # noqa: F401  (re-exported for convenience)

MISSING = object()


@dataclass(frozen=True)
class Ref:
    hid: int

    def __repr__(self):
        return f"@{self.hid}"


@dataclass(frozen=True)
class State:
    ctrl: tuple    # frame stack, top = last element
    env: tuple     # ((name, value), ...) sorted by name; value = int | Ref
    heap: tuple    # ((hid, (cell, ...)), ...) sorted by hid
    next_hid: int
    fault: object = None  # Fault | None

    @property
    def halted(self):
        return self.fault is None and not self.ctrl


def env_get(env, name, default=MISSING):
    for n, v in env:
        if n == name:
            return v
    return default


def env_set(env, name, value):
    items = [(n, v) for n, v in env if n != name]
    items.append((name, value))
    items.sort(key=lambda p: p[0])
    return tuple(items)


def heap_get(heap, hid, default=MISSING):
    for h, cells in heap:
        if h == hid:
            return cells
    return default


def heap_set(heap, hid, cells):
    items = [(h, c) for h, c in heap if h != hid]
    items.append((hid, tuple(cells)))
    items.sort(key=lambda p: p[0])
    return tuple(items)


def normalize(st):
    """Canonicalize heap ids by reachability from the environment.

    Unreachable objects are collected (finite heap stays finite).  The
    renaming follows reference identity, so aliasing structure is preserved
    exactly; only the concrete id numbers change.
    """
    remap = {}
    for _, v in st.env:
        if isinstance(v, Ref) and v.hid not in remap:
            remap[v.hid] = len(remap)
    env = tuple(
        (n, Ref(remap[v.hid]) if isinstance(v, Ref) else v) for n, v in st.env
    )
    heap = tuple(sorted((remap[h], cells) for h, cells in st.heap if h in remap))
    return replace(st, env=env, heap=heap, next_hid=len(remap))


def show_value(v):
    return f"@{v.hid}" if isinstance(v, Ref) else str(v)


def summarize(st):
    env = ", ".join(f"{n}={show_value(v)}" for n, v in st.env)
    heap = ", ".join(f"@{h}={list(cells)}" for h, cells in st.heap)
    tail = f" fault={st.fault.kind}" if st.fault else ""
    return f"env{{{env}}} heap{{{heap}}}{tail}"
