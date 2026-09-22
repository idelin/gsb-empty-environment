"""Validate cached task results against a filesystem change.

A Change is a persistable, self-contained delta: for every touched dir or
object it records the complete resulting state fact.  Verdicts are computed
from evidence + change alone (no live filesystem needed):

  VALID         - every external dependency still holds under the delta
  INVALID       - a concrete dependency is violated (shortest chain reported)
  INSUFFICIENT  - a conservative dependency was triggered; the exact reason
                  is reported (never a blanket "invalidate everything")
"""

from __future__ import annotations

import json

VALID = "valid"
INVALID = "invalid"
INSUFFICIENT = "insufficient"


class Change:
    """Resulting-state facts for every dir/object a change touched."""

    def __init__(self, dirs, objs):
        self.dirs = dirs    # id -> {"version": v, "entries": {name: obj}}
        self.objs = objs    # id -> {"kind","version","content"|"target","readable"}
        self.affected = {("dir", i) for i in dirs} | {("obj", i) for i in objs}

    def to_json_dict(self):
        def enc_obj(f):
            f = dict(f)
            if "content" in f:
                f["content"] = {"hex": f["content"].hex()}
            return f
        return {"dirs": {str(k): v for k, v in self.dirs.items()},
                "objs": {str(k): enc_obj(v) for k, v in self.objs.items()}}

    @staticmethod
    def from_json_dict(d):
        def dec_obj(f):
            f = dict(f)
            if isinstance(f.get("content"), dict):
                f["content"] = bytes.fromhex(f["content"]["hex"])
            return f
        return Change({int(k): v for k, v in d["dirs"].items()},
                      {int(k): dec_obj(v) for k, v in d["objs"].items()})

    def to_json(self):
        return json.dumps(self.to_json_dict(), sort_keys=True)

    @staticmethod
    def from_json(text):
        return Change.from_json_dict(json.loads(text))


def _fact_of(vfs, kind, oid):
    o = vfs.objects[oid]
    if kind == "dir":
        return {"version": o.version, "entries": dict(o.entries)}
    fact = {"kind": o.kind, "version": o.version, "readable": o.readable}
    if o.kind == "file":
        fact["content"] = o.content
    elif o.kind == "symlink":
        fact["target"] = o.target
    return fact


def apply_change(vfs, script):
    """Run mutation *script* (list of op tuples) and capture a rich delta."""
    vfs.effects = []
    for step in script:
        op, args = step[0], step[1:]
        getattr(vfs, op)("__change__", *args)
    dirs, objs = {}, {}
    for kind, oid in vfs.effects:
        if kind == "dir":
            dirs[oid] = _fact_of(vfs, "dir", oid)
        else:
            objs[oid] = _fact_of(vfs, "obj", oid)
    vfs.effects = []
    return Change(dirs, objs)


def _check_dep(dep, change):
    """Return (holds, note); holds is None for conservative insufficiency."""
    kind = dep[0]
    if kind == "walk":
        _, did, dirv, name, obj = dep
        fact = change.dirs[did]
        cur = fact["entries"].get(name)
        if cur == obj:
            return True, ""
        return False, (f"directory {did} now binds {name!r} to {cur} "
                       f"(was {obj} at v{dirv}, now v{fact['version']})")
    if kind == "miss":
        _, did, dirv, name = dep
        fact = change.dirs[did]
        if name not in fact["entries"]:
            return True, ""  # membership changed elsewhere; name still absent
        return False, (f"name {name!r} now present in directory {did} "
                       f"(was absent at v{dirv}); failed lookup no longer valid")
    if kind == "sym":
        _, oid, ver = dep
        fact = change.objs[oid]
        if fact["kind"] == "symlink" and fact["version"] == ver:
            return True, ""
        return False, (f"symlink object {oid} changed (was v{ver}, now "
                       f"v{fact['version']}); resolution itself was a dependency")
    if kind == "obj":
        _, oid, ver = dep
        fact = change.objs[oid]
        if fact["version"] == ver:
            return True, ""
        return False, (f"object {oid} version changed (v{ver}->v{fact['version']}); "
                       f"content/permission observed by the task is stale")
    if kind == "enum":
        _, did, dirv, mode = dep
        fact = change.dirs[did]
        if fact["version"] == dirv:
            return True, ""
        if mode == "prefix":
            return None, (f"directory {did} was only prefix-consumed; membership "
                          f"changed (v{dirv}->v{fact['version']}) and the task may "
                          f"never have observed the new entries")
        return False, (f"directory {did} membership changed "
                       f"(v{dirv}->v{fact['version']}); full enumeration is stale")
    raise ValueError(f"unknown dep {dep!r}")


def _explain(item, note):
    src = item["src"]
    dep = item["dep"]
    where = f"seq {src['seq']} {src['op']}({src['path']!r})"
    if dep[0] == "walk":
        chain = f"dir {dep[1]} entry {dep[3]!r} -> obj {dep[4]}"
    elif dep[0] == "miss":
        chain = f"dir {dep[1]} missing name {dep[3]!r}"
    elif dep[0] == "sym":
        chain = f"symlink obj {dep[1]}"
    elif dep[0] == "obj":
        chain = f"object {dep[1]} @ v{dep[2]}"
    else:
        chain = f"dir {dep[1]} enumeration ({dep[3]})"
    return f"{where}: {chain} -- {note}"


def verdict_for(evidence, change):
    """Return (verdict, explanation_lines) for one task's evidence."""
    affected = change.affected
    violated = []
    insufficient = []
    for item in evidence["deps"]:
        dep = item["dep"]
        key = ("dir", dep[1]) if dep[0] in ("walk", "miss", "enum") else ("obj", dep[1])
        if key not in affected:
            continue  # untouched by the change: holds trivially
        holds, note = _check_dep(dep, change)
        if holds is True:
            continue
        if holds is None:
            insufficient.append(_explain(item, note))
        else:
            violated.append(_explain(item, note))
    if violated:
        return INVALID, ["must invalidate; shortest failing chain:",
                         "  " + violated[0]] + ["  " + v for v in violated[1:]]
    na_hit = [o for o in evidence["non_atomic"] if ("obj", o) in affected]
    if na_hit:
        return INSUFFICIENT, [
            "insufficient evidence: task observed a non-atomic snapshot of "
            f"object(s) {na_hit} (reads spanned a content change) and the "
            "change touches them; cannot prove the cached result reproducible"]
    if insufficient:
        return INSUFFICIENT, ["insufficient evidence (conservative dependency):"] + \
                             ["  " + s for s in insufficient]
    return VALID, [f"cache valid: all {len(evidence['deps'])} dependencies "
                   "verified against the change delta"]
