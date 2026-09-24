"""Serializable snapshots of suspended coroutines.

A snapshot captures the full suspended state: the shared closure
environments (as a graph, preserving sharing and cycles), the call
frames with operand stacks, and the pending try/finally handlers.

Host (builtin) functions are never serialized: encountering one raises
SnapshotError. The global environment is stored as a marker and
re-attached to the target machine's globals on restore.

Restore validates the bytecode version and the program digest, so a
snapshot silently outliving its program is impossible.
"""

import json

from .compiler import BC_VERSION
from .runtime import (
    Closure,
    Coroutine,
    Env,
    Frame,
    GuestIter,
    Handler,
    SnapshotError,
    _Cancel,
)

FORMAT = "minivm-snapshot"


def _is_lit(value):
    return value is None or isinstance(value, (bool, int, float, str))


class _Serializer:
    def __init__(self, program):
        self.program = program
        self.env_ids = {}
        self.envs = []
        self.list_ids = {}
        self.lists = []

    def env_ref(self, env):
        if env is None:
            return None
        if env.is_global:
            return {"g": True}
        key = id(env)
        if key in self.env_ids:
            return {"e": self.env_ids[key]}
        nid = len(self.envs)
        self.env_ids[key] = nid
        self.envs.append(None)  # placeholder: allows cyclic env graphs
        self.envs[nid] = {
            "parent": self.env_ref(env.parent),
            "cells": [self.value(v) for v in env.cells],
        }
        return {"e": nid}

    def value(self, v):
        if _is_lit(v):
            return {"t": "lit", "v": v}
        if isinstance(v, Closure):
            return {
                "t": "closure",
                "code": self.program.codes.index(v.code),
                "env": self.env_ref(v.env),
            }
        if isinstance(v, GuestIter):
            return {"t": "iter", "items": self.value(v.items), "index": v.index}
        if isinstance(v, list):
            key = id(v)
            if key in self.list_ids:
                return {"t": "list", "id": self.list_ids[key]}
            nid = len(self.lists)
            self.list_ids[key] = nid
            self.lists.append(None)
            self.lists[nid] = {"items": [self.value(x) for x in v]}
            return {"t": "list", "id": nid}
        if callable(v):
            raise SnapshotError(
                f"cannot serialize host function {getattr(v, '__name__', v)!r}"
            )
        raise SnapshotError(f"unserializable value of type {type(v).__name__}")

    def pending(self, pending):
        if pending is None:
            return None
        exc, catchable = pending
        if isinstance(exc, _Cancel):
            raise SnapshotError("cannot snapshot while cancelling")
        return {"exc": self.value(exc), "catchable": catchable}

    def frame(self, frame):
        return {
            "code": self.program.codes.index(frame.code),
            "ip": frame.ip,
            "env": self.env_ref(frame.env),
            "stack": [self.value(v) for v in frame.stack],
            "handlers": [
                {
                    "catch": h.catch,
                    "finally": h.finally_addr,
                    "depth": h.depth,
                    "env": self.env_ref(h.env),
                }
                for h in frame.handlers
            ],
            "pending": self.pending(frame.pending),
        }


def to_data(co):
    """Serialize a suspended coroutine to a JSON-able dict."""
    if co.status not in ("suspended", "new"):
        raise SnapshotError(
            f"can only snapshot a suspended coroutine, not {co.status!r}"
        )
    ser = _Serializer(co.machine.program)
    frames = [ser.frame(f) for f in co.frames]
    return {
        "format": FORMAT,
        "version": BC_VERSION,
        "digest": co.machine.program.digest,
        "status": co.status,
        "yielded": ser.value(co.yielded),
        "envs": ser.envs,
        "lists": ser.lists,
        "frames": frames,
    }


def dumps(co, **json_kwargs):
    return json.dumps(to_data(co), **json_kwargs)


class _Deserializer:
    def __init__(self, machine, data):
        self.machine = machine
        self.data = data
        self.envs = [None] * len(data["envs"])
        self.lists = [None] * len(data["lists"])

    def env(self, ref):
        if ref is None:
            return None
        if ref.get("g"):
            return self.machine.global_env
        idx = ref["e"]
        if self.envs[idx] is None:
            entry = self.data["envs"][idx]
            env = Env([], self.env(entry["parent"]))
            self.envs[idx] = env  # register before cells: env graphs may cycle
            env.cells = [self.value(v) for v in entry["cells"]]
        return self.envs[idx]

    def list_(self, idx):
        if self.lists[idx] is None:
            lst = []
            self.lists[idx] = lst
            lst.extend(self.value(v) for v in self.data["lists"][idx]["items"])
        return self.lists[idx]

    def value(self, d):
        t = d["t"]
        if t == "lit":
            return d["v"]
        if t == "closure":
            return Closure(self.machine.program.codes[d["code"]], self.env(d["env"]))
        if t == "list":
            return self.list_(d["id"])
        if t == "iter":
            return GuestIter(self.value(d["items"]), d["index"])
        raise SnapshotError(f"bad value tag: {t!r}")

    def frames(self):
        frames = []
        for fd in self.data["frames"]:
            pending = fd["pending"]
            if pending is not None:
                pending = (self.value(pending["exc"]), pending["catchable"])
            frames.append(
                Frame(
                    self.machine.program.codes[fd["code"]],
                    fd["ip"],
                    [self.value(v) for v in fd["stack"]],
                    self.env(fd["env"]),
                    [
                        Handler(h["catch"], h["finally"], h["depth"], self.env(h["env"]))
                        for h in fd["handlers"]
                    ],
                    pending,
                )
            )
        return frames


def restore(machine, data):
    """Rebuild a coroutine from snapshot data on the given machine.

    Raises SnapshotError if the snapshot does not match the machine's
    program (bytecode version or program digest mismatch).
    """
    if isinstance(data, str):
        try:
            data = json.loads(data)
        except json.JSONDecodeError as exc:
            raise SnapshotError(f"invalid snapshot JSON: {exc}") from None
    if not isinstance(data, dict) or data.get("format") != FORMAT:
        raise SnapshotError("not a minivm snapshot")
    if data.get("version") != BC_VERSION:
        raise SnapshotError(
            f"bytecode version mismatch: snapshot={data.get('version')} "
            f"runtime={BC_VERSION}"
        )
    if data.get("digest") != machine.program.digest:
        raise SnapshotError("program digest mismatch: program changed since snapshot")
    if data.get("status") not in ("suspended", "new"):
        raise SnapshotError(f"bad snapshot status: {data.get('status')!r}")
    de = _Deserializer(machine, data)
    co = Coroutine(machine)
    co.frames = de.frames()
    co.status = data["status"]
    co.yielded = de.value(data["yielded"])
    return co


def loads(machine, s):
    return restore(machine, s)
