"""Compress a task's trace events into persistable dependency evidence.

A dependency is a small canonical tuple (JSON-serialisable as a list):

  ["walk", dir, dirv, name, obj]   name was bound to obj inside dir@dirv
  ["miss", dir, dirv, name]        name was absent from dir@dirv
  ["sym",  obj, ver]               symlink object version observed
  ["obj",  obj, ver]               object content/perm version observed
  ["enum", dir, dirv, mode]        dir membership enumerated (full|prefix)

Read-your-own-writes, soundly: a dep whose key the task itself mutated is
rewritten to the key's *base version* — the external version observed before
the task's first mutation of that key.  Deps on objects the task itself
created are internal and dropped (they are grounded by the miss dep recorded
at creation).  Each surviving dep carries a "src" back-reference (seq, op,
path) so the query layer can render the shortest explanation chain.  Objects
observed at more than one external version are recorded as ``non_atomic``
(an inconsistent snapshot that must never approve a cache).
"""

from __future__ import annotations

import json


def _src(ev):
    return {"seq": ev["seq"], "op": ev["op"],
            "path": ev.get("path") or ev.get("src") or ""}


def _key_of(dep):
    if dep[0] in ("walk", "miss", "enum"):
        return ("dir", dep[1])
    return ("obj", dep[1])


def _chain_deps(chain):
    for step in chain:
        if step["t"] == "walk":
            yield ["walk", step["dir"], step["dirv"], step["name"], step["obj"]]
        elif step["t"] == "sym":
            yield ["sym", step["obj"], step["ver"]]


def _result_deps(ev):
    res = ev.get("result") or {}
    op = ev["op"]
    miss = res.get("miss") or (res.get("ok") or {}).get("miss")
    if miss:
        yield ["miss", miss["dir"], miss["dirv"], miss["name"]]
    for m in res.get("mem", []):
        if m["obj"] is None:
            yield ["miss", m["dir"], m["dirv"], m["name"]]
        else:
            yield ["walk", m["dir"], m["dirv"], m["name"], m["obj"]]
    if op == "open":
        if "ok" in res:
            if res["ok"].get("kind") == "dir":
                # a directory's version is its membership version
                yield ["enum", res["ok"]["obj"], res["ok"]["ver"], "open"]
            else:
                yield ["obj", res["ok"]["obj"], res["ok"]["ver"]]
        elif res.get("error") in ("EACCES", "ENOTDIR") and "obj" in res:
            yield ["obj", res["obj"], res["ver"]]
    elif op == "read":
        if "ok" in res:
            if ev.get("kind") == "dir":
                yield ["enum", ev["obj"], res["ok"]["ver"], "open"]
            else:
                yield ["obj", ev["obj"], res["ok"]["ver"]]
        elif res.get("error") == "EACCES":
            yield ["obj", ev["obj"], res["ver"]]
    elif op == "enumdir":
        if "ok" in res:
            ok = res["ok"]
            yield ["enum", ok["dir"], ok["ver"], ok["mode"]]
        elif res.get("error") == "EACCES" and "obj" in res:
            yield ["obj", res["obj"], res["ver"]]
    if op in ("write", "chmod") and "ok" in res and "pre_ver" in res["ok"]:
        # the pre-mutation object state is a genuine external observation
        yield ["obj", res["ok"]["obj"], res["ok"]["pre_ver"]]


def build_evidence(task_id, events):
    """Build compressed evidence for *task_id* from its trace events."""
    deps = {}
    obj_versions = {}
    self_touched = set()
    base = {}  # key -> external version before the task's first mutation
    for ev in events:
        src = _src(ev)
        for dep in list(_chain_deps(ev.get("chain", []))) + list(_result_deps(ev)):
            key = _key_of(dep)
            if key in self_touched:
                if key not in base:
                    continue  # task-created object: internal observation
                dep = dep[:2] + [base[key]] + dep[3:]
            else:
                base.setdefault(key, dep[2])
            dkey = tuple(dep)
            if dkey not in deps or src["seq"] < deps[dkey]["src"]["seq"]:
                deps[dkey] = {"dep": dep, "src": src}
            if dep[0] == "obj":
                obj_versions.setdefault(dep[1], set()).add(dep[2])
        for fx in ev.get("fx", []):
            self_touched.add((fx[0], fx[1]))
    ordered = sorted(deps.values(), key=lambda d: (d["src"]["seq"], d["dep"]))
    return {
        "task": task_id,
        "deps": ordered,
        "non_atomic": sorted(o for o, vs in obj_versions.items() if len(vs) > 1),
        "ops": len(events),
    }


def dep_keys(evidence):
    """Reverse-index keys: ("dir", id) or ("obj", id) for every dep."""
    keys = set()
    for item in evidence["deps"]:
        keys.add(_key_of(item["dep"]))
    for oid in evidence["non_atomic"]:
        keys.add(("obj", oid))
    return keys


def evidence_to_json(evidence):
    return json.dumps(evidence, sort_keys=True, separators=(",", ":"))


def evidence_from_json(text):
    ev = json.loads(text)
    for item in ev["deps"]:
        item["dep"] = list(item["dep"])
    return ev
