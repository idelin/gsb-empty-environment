"""Synthetic virtual filesystem with stable object identity and content versions.

Paths are not identity.  A successful open binds a stable object id plus the
content version observed at that moment.  Renaming or unlinking a path never
touches already-open handles: they keep pointing at the original object.
Hard links share one object; symlinks are separate objects whose resolution
is itself recorded as a dependency.

Every operation appends an event to ``self.log``; mutations additionally
append ("dir", id) / ("obj", id) effect records to ``self.effects`` so that a
later change-set can be diffed against recorded dependencies without
replaying anything.
"""

from __future__ import annotations

import copy
import hashlib

MAX_SYMLINK_HOPS = 8


class FileObj:
    kind = "file"

    def __init__(self, oid, content=b"", readable=True):
        self.id = oid
        self.version = 1
        self.content = content
        self.readable = readable


class DirObj:
    kind = "dir"

    def __init__(self, oid, readable=True):
        self.id = oid
        self.version = 1
        self.entries = {}  # name -> object id
        self.readable = readable


class SymlinkObj:
    kind = "symlink"

    def __init__(self, oid, target):
        self.id = oid
        self.version = 1
        self.target = target  # absolute path string
        self.readable = True


def _split(path):
    return [c for c in path.split("/") if c]


def _dirname(path):
    parts = _split(path)
    return "/" + "/".join(parts[:-1]) if parts[:-1] else "/"


def _basename(path):
    parts = _split(path)
    return parts[-1] if parts else ""


def content_hash(data):
    return hashlib.sha1(data).hexdigest()[:12]


class VFS:
    def __init__(self):
        self.objects = {0: DirObj(0)}
        self._next_id = 1
        self.handles = {}          # handle id -> object id
        self._next_handle = 1
        self.seq = 0
        self.log = []              # all events, in order
        self.effects = []          # ("dir"|"obj", id) touched since last reset

    # ------------------------------------------------------------------ util
    def _alloc(self, obj):
        obj.id = self._next_id
        self.objects[obj.id] = obj
        self._next_id += 1
        self.effects.append(("obj", obj.id))
        if obj.kind == "dir":
            self.effects.append(("dir", obj.id))
        return obj

    def _emit(self, task, op, **kw):
        self.seq += 1
        ev = {"seq": self.seq, "task": task, "op": op}
        ev.update(kw)
        self.log.append(ev)
        return ev

    def _touch_dir(self, d):
        d.version += 1
        self.effects.append(("dir", d.id))

    def _touch_obj(self, o):
        o.version += 1
        self.effects.append(("obj", o.id))

    def _fx_since(self, mark):
        return [list(x) for x in self.effects[mark:]]

    def snapshot(self):
        """Deep copy suitable for the independent oracle replayer."""
        return copy.deepcopy(self)

    # ------------------------------------------------------------ resolution
    def resolve(self, path, follow_final=True):
        """Resolve *path*, recording every step actually taken.

        Returns (chain, terminal).  Chain steps:
          {"t":"walk","dir":d,"dirv":v,"name":n,"obj":o}
          {"t":"sym","obj":s,"ver":v,"target":t}
        Terminals:
          {"t":"ok","obj":o,"ver":v,"kind":k}
          {"t":"miss","dir":d,"dirv":v,"name":n}      # parent dir + missing name
          {"t":"deny","obj":o,"ver":v}                # permission denied
          {"t":"notdir","obj":o,"ver":v}
          {"t":"loop"}
        """
        chain = []
        comps = _split(path)
        if not comps:
            root = self.objects[0]
            return chain, {"t": "ok", "obj": 0, "ver": root.version, "kind": "dir"}
        cur = 0
        i = 0
        hops = 0
        while True:
            d = self.objects[cur]
            name = comps[i]
            final = i == len(comps) - 1
            if name not in d.entries:
                return chain, {"t": "miss", "dir": d.id, "dirv": d.version, "name": name}
            oid = d.entries[name]
            o = self.objects[oid]
            chain.append({"t": "walk", "dir": d.id, "dirv": d.version,
                          "name": name, "obj": oid})
            if o.kind == "symlink" and (follow_final or not final):
                hops += 1
                chain.append({"t": "sym", "obj": oid, "ver": o.version,
                              "target": o.target})
                if hops > MAX_SYMLINK_HOPS:
                    return chain, {"t": "loop"}
                comps = _split(o.target) + comps[i + 1:]
                cur = 0
                i = 0
                continue
            if not final:
                if o.kind != "dir":
                    return chain, {"t": "notdir", "obj": oid, "ver": o.version}
                if not o.readable:
                    return chain, {"t": "deny", "obj": oid, "ver": o.version}
                cur = oid
                i += 1
                continue
            return chain, {"t": "ok", "obj": oid, "ver": o.version, "kind": o.kind}

    # ------------------------------------------------------------- read ops
    def open(self, task, path):
        chain, term = self.resolve(path)
        if term["t"] == "ok":
            o = self.objects[term["obj"]]
            if o.kind == "file" and not o.readable:
                ev = self._emit(task, "open", path=path, chain=chain,
                                result={"error": "EACCES", "obj": o.id, "ver": o.version})
                return ev, None
            h = self._next_handle
            self._next_handle += 1
            self.handles[h] = o.id
            ev = self._emit(task, "open", path=path, chain=chain,
                            result={"ok": {"obj": o.id, "ver": o.version,
                                           "handle": h, "kind": o.kind}})
            return ev, h
        if term["t"] == "miss":
            res = {"error": "ENOENT", "miss": term}
        elif term["t"] == "deny":
            res = {"error": "EACCES", "obj": term["obj"], "ver": term["ver"]}
        elif term["t"] == "notdir":
            res = {"error": "ENOTDIR", "obj": term["obj"], "ver": term["ver"]}
        else:
            res = {"error": "ELOOP"}
        ev = self._emit(task, "open", path=path, chain=chain, result=res)
        return ev, None

    def read(self, task, handle):
        oid = self.handles[handle]
        o = self.objects[oid]
        if o.kind == "file" and not o.readable:
            return self._emit(task, "read", handle=handle, obj=oid,
                              result={"error": "EACCES", "ver": o.version})
        data = o.content if o.kind == "file" else ("@" + o.target).encode() \
            if o.kind == "symlink" else b"."
        return self._emit(task, "read", handle=handle, obj=oid, kind=o.kind,
                          result={"ok": {"ver": o.version, "hash": content_hash(data)}})

    def enumdir(self, task, path, limit=None):
        chain, term = self.resolve(path)
        if term["t"] != "ok" or term["kind"] != "dir":
            err = "ENOENT" if term["t"] == "miss" else "ENOTDIR"
            res = {"error": err}
            if term["t"] == "miss":
                res["miss"] = term
            return self._emit(task, "enumdir", path=path, chain=chain, result=res)
        d = self.objects[term["obj"]]
        if not d.readable:
            return self._emit(task, "enumdir", path=path, chain=chain,
                              result={"error": "EACCES", "obj": d.id, "ver": d.version})
        names = sorted(d.entries)
        if limit is not None and limit < len(names):
            mode, shown = "prefix", names[:limit]
        else:
            mode, shown = "full", names
        return self._emit(task, "enumdir", path=path, chain=chain,
                          result={"ok": {"dir": d.id, "ver": d.version,
                                         "entries": shown, "mode": mode}})

    # ------------------------------------------------------------ mutations
    def write(self, task, path, content):
        _mark = len(self.effects)
        chain, term = self.resolve(path)
        if term["t"] == "ok" and term["kind"] == "file":
            o = self.objects[term["obj"]]
            pre_ver = o.version
            o.content = content
            self._touch_obj(o)
            return self._emit(task, "write", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"ok": {"obj": o.id, "ver": o.version,
                                             "pre_ver": pre_ver}})
        if term["t"] == "miss" and term["name"] == _basename(path):
            parent = self.objects[term["dir"]]
            o = self._alloc(FileObj(0, content))
            parent.entries[term["name"]] = o.id
            self._touch_dir(parent)
            return self._emit(task, "write", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"ok": {"obj": o.id, "ver": o.version,
                                             "created": True, "miss": term}})
        return self._emit(task, "write", fx=self._fx_since(_mark), path=path, chain=chain,
                          result={"error": "ENOENT"})

    def mkdir(self, task, path):
        _mark = len(self.effects)
        chain, term = self.resolve(path)
        if term["t"] == "ok":
            return self._emit(task, "mkdir", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"error": "EEXIST"})
        if term["t"] == "miss" and term["name"] == _basename(path):
            parent = self.objects[term["dir"]]
            o = self._alloc(DirObj(0))
            parent.entries[term["name"]] = o.id
            self._touch_dir(parent)
            return self._emit(task, "mkdir", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"ok": {"obj": o.id, "created": True,
                                             "miss": term}})
        return self._emit(task, "mkdir", fx=self._fx_since(_mark), path=path, chain=chain,
                          result={"error": "ENOENT"})

    def rename(self, task, src, dst):
        _mark = len(self.effects)
        sdir_path, sname = _dirname(src), _basename(src)
        ddir_path, dname = _dirname(dst), _basename(dst)
        c1, t1 = self.resolve(sdir_path)
        c2, t2 = self.resolve(ddir_path)
        chain = c1 + c2
        if t1["t"] != "ok" or t2["t"] != "ok":
            return self._emit(task, "rename", fx=self._fx_since(_mark), src=src, dst=dst, chain=chain,
                              result={"error": "ENOENT"})
        sd, dd = self.objects[t1["obj"]], self.objects[t2["obj"]]
        if sname not in sd.entries:
            return self._emit(task, "rename", fx=self._fx_since(_mark), src=src, dst=dst, chain=chain,
                              result={"error": "ENOENT",
                                      "miss": {"dir": sd.id, "dirv": sd.version,
                                               "name": sname}})
        mem = [{"dir": sd.id, "dirv": sd.version, "name": sname,
                "obj": sd.entries[sname]},
               {"dir": dd.id, "dirv": dd.version, "name": dname,
                "obj": dd.entries.get(dname)}]
        oid = sd.entries.pop(sname)
        self._touch_dir(sd)
        if dname in dd.entries:
            self._touch_dir(dd)  # replaced existing entry
        dd.entries[dname] = oid
        self._touch_dir(dd)
        return self._emit(task, "rename", fx=self._fx_since(_mark), src=src, dst=dst, chain=chain,
                          result={"ok": {"obj": oid}, "mem": mem})

    def link(self, task, src, dst):
        _mark = len(self.effects)
        c1, t1 = self.resolve(src)
        ddir_path, dname = _dirname(dst), _basename(dst)
        c2, t2 = self.resolve(ddir_path)
        chain = c1 + c2
        if t1["t"] != "ok" or t1["kind"] == "dir" or t2["t"] != "ok":
            return self._emit(task, "link", fx=self._fx_since(_mark), src=src, dst=dst, chain=chain,
                              result={"error": "ENOENT"})
        dd = self.objects[t2["obj"]]
        mem = [{"dir": dd.id, "dirv": dd.version, "name": dname,
                "obj": dd.entries.get(dname)}]
        dd.entries[dname] = t1["obj"]
        self._touch_dir(dd)
        return self._emit(task, "link", fx=self._fx_since(_mark), src=src, dst=dst, chain=chain,
                          result={"ok": {"obj": t1["obj"]}, "mem": mem})

    def symlink(self, task, target, path):
        _mark = len(self.effects)
        ddir_path, name = _dirname(path), _basename(path)
        chain, term = self.resolve(ddir_path)
        if term["t"] != "ok":
            return self._emit(task, "symlink", fx=self._fx_since(_mark), target=target, path=path,
                              chain=chain, result={"error": "ENOENT"})
        dd = self.objects[term["obj"]]
        mem = [{"dir": dd.id, "dirv": dd.version, "name": name,
                "obj": dd.entries.get(name)}]
        o = self._alloc(SymlinkObj(0, target))
        if name in dd.entries:
            self._touch_dir(dd)
        dd.entries[name] = o.id
        self._touch_dir(dd)
        return self._emit(task, "symlink", fx=self._fx_since(_mark), target=target, path=path,
                          chain=chain, result={"ok": {"obj": o.id}, "mem": mem})

    def unlink(self, task, path):
        _mark = len(self.effects)
        ddir_path, name = _dirname(path), _basename(path)
        chain, term = self.resolve(ddir_path)
        if term["t"] != "ok":
            return self._emit(task, "unlink", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"error": "ENOENT"})
        dd = self.objects[term["obj"]]
        if name not in dd.entries:
            return self._emit(task, "unlink", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"error": "ENOENT",
                                      "miss": {"dir": dd.id, "dirv": dd.version,
                                               "name": name}})
        mem = [{"dir": dd.id, "dirv": dd.version, "name": name,
                "obj": dd.entries[name]}]
        oid = dd.entries.pop(name)
        self._touch_dir(dd)
        return self._emit(task, "unlink", fx=self._fx_since(_mark), path=path, chain=chain,
                          result={"ok": {"obj": oid}, "mem": mem})

    def set_readable(self, task, path, flag):
        _mark = len(self.effects)
        chain, term = self.resolve(path)
        if term["t"] != "ok":
            return self._emit(task, "chmod", fx=self._fx_since(_mark), path=path, chain=chain,
                              result={"error": "ENOENT"})
        o = self.objects[term["obj"]]
        pre_ver = o.version
        o.readable = bool(flag)
        self._touch_obj(o)
        return self._emit(task, "chmod", fx=self._fx_since(_mark), path=path, chain=chain, flag=bool(flag),
                          result={"ok": {"obj": o.id, "ver": o.version,
                                          "pre_ver": pre_ver}})
