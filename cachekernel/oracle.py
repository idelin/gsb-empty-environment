"""Independent oracle: replay a task's recorded trace on a changed VFS.

This is the ground-truth criterion for the tests.  It deliberately does NOT
reuse the kernel's dependency logic: it walks paths with its own resolver,
applies the change's resulting-state facts to a deep copy of the pre-task
VFS, re-executes every recorded operation, and compares every observable
output against the recorded event.  If all outputs match, the cached result
was truly still valid.
"""

from __future__ import annotations

from .vfs import FileObj, DirObj, SymlinkObj, content_hash


def _comps(path):
    return [c for c in path.split("/") if c]


def _walk(vfs, path, follow_final=True):
    """Independent resolver.  Returns (obj_id, error)."""
    comps = _comps(path)
    if not comps:
        return 0, None
    cur, i, hops = 0, 0, 0
    while True:
        d = vfs.objects[cur]
        name = comps[i]
        if name not in d.entries:
            return None, "ENOENT"
        oid = d.entries[name]
        o = vfs.objects[oid]
        final = i == len(comps) - 1
        if o.kind == "symlink" and (follow_final or not final):
            hops += 1
            if hops > 8:
                return None, "ELOOP"
            comps = _comps(o.target) + comps[i + 1:]
            cur, i = 0, 0
            continue
        if not final:
            if o.kind != "dir":
                return None, "ENOTDIR"
            if not o.readable:
                return None, "EACCES"
            cur, i = oid, i + 1
            continue
        return oid, None


def _parent(vfs, path):
    comps = _comps(path)
    if not comps:
        return None, None, "ENOENT"
    oid, err = _walk(vfs, "/" + "/".join(comps[:-1]) if comps[:-1] else "/")
    return oid, comps[-1], err


def _apply_facts(vfs, change):
    for did, fact in change.dirs.items():
        d = vfs.objects.get(did)
        if d is None:
            d = DirObj(did)
            vfs.objects[did] = d
        d.entries = dict(fact["entries"])
        d.version = fact["version"]
        vfs._next_id = max(vfs._next_id, did + 1)
    for oid, fact in change.objs.items():
        o = vfs.objects.get(oid)
        if o is None:
            if fact["kind"] == "file":
                o = FileObj(oid, fact["content"], fact["readable"])
            elif fact["kind"] == "symlink":
                o = SymlinkObj(oid, fact["target"])
            else:
                o = DirObj(oid, fact["readable"])
            vfs.objects[oid] = o
        o.version = fact["version"]
        o.readable = fact["readable"]
        if o.kind == "file":
            o.content = fact["content"]
        elif o.kind == "symlink":
            o.target = fact["target"]
        vfs._next_id = max(vfs._next_id, oid + 1)


def replay(snapshot, change, events):
    """Replay *events* on snapshot+change facts.  Returns (match, note)."""
    vfs = snapshot.snapshot()
    base_id = vfs._next_id
    _apply_facts(vfs, change)
    handles = {}
    self_touched = set()  # ids the replayed task itself mutated

    def fresh(oid):
        return oid is not None and oid >= base_id

    for ev in events:
        op = ev["op"]
        res = ev.get("result") or {}
        if op == "open":
            oid, err = _walk(vfs, ev["path"])
            if err is None and vfs.objects[oid].kind == "file" \
                    and not vfs.objects[oid].readable:
                err, oid = "EACCES", oid
            if "ok" in res:
                if err is not None:
                    return False, f"open {ev['path']}: recorded ok, replay {err}"
                o = vfs.objects[oid]
                want = res["ok"]
                if not fresh(want["obj"]) and want["obj"] != oid:
                    return False, (f"open {ev['path']}: recorded obj {want['obj']}, "
                                   f"replay obj {oid}")
                if (not fresh(want["obj"]) and oid not in self_touched
                        and want["ver"] != o.version):
                    return False, (f"open {ev['path']}: recorded v{want['ver']}, "
                                   f"replay v{o.version}")
                handles[len(handles) + 1] = oid
            else:
                if err is None:
                    return False, f"open {ev['path']}: recorded {res['error']}, replay ok"
                if err != res["error"]:
                    return False, (f"open {ev['path']}: recorded {res['error']}, "
                                   f"replay {err}")
        elif op == "read":
            oid = handles.get(ev["handle"])
            if oid is None:
                return False, "read: unknown handle in replay"
            o = vfs.objects[oid]
            if "ok" in res:
                data = o.content if o.kind == "file" else b"."
                if content_hash(data) != res["ok"]["hash"]:
                    return False, f"read obj {oid}: content hash differs"
            elif o.kind != "file" or o.readable:
                return False, f"read obj {oid}: recorded EACCES, replay ok"
        elif op == "enumdir":
            oid, err = _walk(vfs, ev["path"])
            if "ok" in res:
                if err is not None or vfs.objects[oid].kind != "dir":
                    return False, f"enumdir {ev['path']}: recorded ok, replay {err}"
                d = vfs.objects[oid]
                if not d.readable:
                    return False, f"enumdir {ev['path']}: recorded ok, replay EACCES"
                shown = sorted(d.entries)[:len(res["ok"]["entries"])]
                if shown != res["ok"]["entries"]:
                    return False, (f"enumdir {ev['path']}: recorded "
                                   f"{res['ok']['entries']}, replay {shown}")
            elif err is None and vfs.objects[oid].kind == "dir" \
                    and vfs.objects[oid].readable:
                return False, f"enumdir {ev['path']}: recorded error, replay ok"
        elif op == "write":
            oid, err = _walk(vfs, ev["path"])
            ok = "ok" in res
            if ok:
                content = _recorded_write_content(vfs, ev, snapshot, change)
                if err is None and vfs.objects[oid].kind == "file":
                    vfs.objects[oid].content = content
                    vfs.objects[oid].version += 1
                    self_touched.add(oid)
                elif err == "ENOENT":
                    pid, name, perr = _parent(vfs, ev["path"])
                    if perr is not None:
                        return False, f"write {ev['path']}: replay parent missing"
                    no = FileObj(vfs._next_id, content)
                    vfs.objects[no.id] = no
                    vfs._next_id += 1
                    vfs.objects[pid].entries[name] = no.id
                    vfs.objects[pid].version += 1
                    self_touched.add(pid)
                else:
                    return False, f"write {ev['path']}: recorded ok, replay {err}"
            elif err is None:
                return False, f"write {ev['path']}: recorded error, replay ok"
        elif op in ("rename", "link", "symlink", "unlink", "chmod", "mkdir"):
            note = _replay_mutation(vfs, op, ev, self_touched)
            if note is not None:
                return False, note
        else:
            return False, f"unknown op {op}"
    return True, "all recorded outputs reproduced"


def _recorded_write_content(vfs, ev, snapshot, change_script):
    # Write contents are not stored in events; tests use fixed content per
    # path so the oracle can derive it deterministically.
    return ("data:" + ev["path"]).encode()


def _replay_mutation(vfs, op, ev, self_touched):
    res = ev.get("result") or {}
    want_ok = "ok" in res
    if op == "mkdir":
        dp, dn, perr = _parent(vfs, ev["path"])
        ok = perr is None and dn not in vfs.objects[dp].entries
        if ok:
            no = DirObj(vfs._next_id)
            vfs.objects[no.id] = no
            vfs._next_id += 1
            vfs.objects[dp].entries[dn] = no.id
            vfs.objects[dp].version += 1
            self_touched.add(dp)
    elif op == "rename":
        sp, sn, serr = _parent(vfs, ev["src"])
        dp, dn, derr = _parent(vfs, ev["dst"])
        ok = (serr is None and derr is None
              and sn in vfs.objects[sp].entries)
        if ok:
            oid = vfs.objects[sp].entries.pop(sn)
            vfs.objects[sp].version += 1
            if dn in vfs.objects[dp].entries:
                vfs.objects[dp].version += 1
            vfs.objects[dp].entries[dn] = oid
            vfs.objects[dp].version += 1
            self_touched.update((sp, dp))
    elif op == "link":
        oid, err = _walk(vfs, ev["src"])
        dp, dn, perr = _parent(vfs, ev["dst"])
        ok = err is None and perr is None and vfs.objects[oid].kind != "dir"
        if ok:
            vfs.objects[dp].entries[dn] = oid
            vfs.objects[dp].version += 1
            self_touched.add(dp)
    elif op == "symlink":
        dp, dn, perr = _parent(vfs, ev["path"])
        ok = perr is None
        if ok:
            no = SymlinkObj(vfs._next_id, ev["target"])
            vfs.objects[no.id] = no
            vfs._next_id += 1
            if dn in vfs.objects[dp].entries:
                vfs.objects[dp].version += 1
            vfs.objects[dp].entries[dn] = no.id
            vfs.objects[dp].version += 1
            self_touched.add(dp)
    elif op == "unlink":
        dp, dn, perr = _parent(vfs, ev["path"])
        ok = perr is None and dn in vfs.objects[dp].entries
        if ok:
            del vfs.objects[dp].entries[dn]
            vfs.objects[dp].version += 1
            self_touched.add(dp)
    elif op == "chmod":
        oid, err = _walk(vfs, ev["path"])
        ok = err is None
        if ok:
            vfs.objects[oid].readable = bool(ev["flag"])
            vfs.objects[oid].version += 1
            self_touched.add(oid)
    if ok != want_ok:
        return f"{op}: recorded ok={want_ok}, replay ok={ok}"
    return None
