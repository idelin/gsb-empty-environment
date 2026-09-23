"""Three-way merge for package lock files.

The merge is fully offline. The local package registry is used only to
re-verify the dependency closure; versions are never resolved or downloaded.
"""

from __future__ import annotations

from .constraints import Constraint
from .models import Lockfile, Registry, RegistryError
from .version import Version


def merge_locks(base: Lockfile, ours: Lockfile, theirs: Lockfile,
                registry: Registry) -> "MergeResult":
    merger = _Merger(base, ours, theirs, registry)
    return merger.run()


class MergeResult:
    def __init__(self) -> None:
        self.merged_lock: dict = {}
        self.sources: dict = {"roots": {}, "build_inputs": {}, "packages": {}}
        self.conflict_hunks: list[dict] = []
        self.rejected_deletions: list[dict] = []
        self.closure_violations: list[dict] = []
        self.warnings: list[dict] = []
        self.unprocessable: list[dict] = []

    @property
    def status(self) -> str:
        return "merged" if not self.unprocessable else "needs_manual"

    def to_dict(self) -> dict:
        return {
            "status": self.status,
            "merged_lock": self.merged_lock,
            "sources": self.sources,
            "conflict_hunks": self.conflict_hunks,
            "rejected_deletions": self.rejected_deletions,
            "closure_violations": self.closure_violations,
            "warnings": self.warnings,
            "unprocessable": self.unprocessable,
        }


class _Merger:
    def __init__(self, base: Lockfile, ours: Lockfile, theirs: Lockfile,
                 registry: Registry) -> None:
        self.base, self.ours, self.theirs, self.registry = base, ours, theirs, registry
        self.result = MergeResult()
        # entries: name -> entry dict (None means tentatively deleted)
        self.entries: dict[str, dict | None] = {}
        self.deleted_by: dict[str, str] = {}  # name -> side that cleanly deleted

    def run(self) -> MergeResult:
        roots = self._merge_scalars(
            self.base.roots, self.ours.roots, self.theirs.roots, "root")
        inputs = self._merge_scalars(
            self.base.build_inputs, self.ours.build_inputs,
            self.theirs.build_inputs, "build_input")
        self._merge_package_entries()
        self._reject_dangling_deletions(roots)

        merged = {
            "roots": dict(sorted(roots.items())),
            "build_inputs": dict(sorted(inputs.items())),
            "packages": {
                name: dict(entry)
                for name, entry in sorted(self.entries.items())
                if entry is not None
            },
        }
        self.result.merged_lock = merged
        self._verify_closure(merged)
        self._collect_unprocessable()
        return self.result

    # ------------------------------------------------------------------
    # Scalar merges (roots and build input pins)
    # ------------------------------------------------------------------
    def _merge_scalars(self, b: dict, o: dict, t: dict, kind: str) -> dict:
        out: dict[str, str] = {}
        src = (self.result.sources["roots"] if kind == "root"
               else self.result.sources["build_inputs"])
        for key in sorted(set(b) | set(o) | set(t)):
            bv, ov, tv = b.get(key), o.get(key), t.get(key)
            if ov == tv:
                if ov is not None:
                    out[key] = ov
                src[key] = "both" if bv != ov else "base"
            elif bv == ov and tv != bv:
                if tv is not None:
                    out[key] = tv
                src[key] = "theirs" if tv is not None else "theirs-deleted"
            elif bv == tv and ov != bv:
                if ov is not None:
                    out[key] = ov
                src[key] = "ours" if ov is not None else "ours-deleted"
            else:
                # Both sides changed the same key to different values.
                if bv is not None:
                    out[key] = bv  # never splice either change blindly
                elif ov is not None:
                    out[key] = ov
                self._conflict(
                    f"{kind}_conflict", key,
                    {"base": bv, "ours": ov, "theirs": tv, "kept": out.get(key)})
                src[key] = "conflict"
        return out

    # ------------------------------------------------------------------
    # Package entry merges
    # ------------------------------------------------------------------
    def _merge_package_entries(self) -> None:
        b, o, t = self.base.packages, self.ours.packages, self.theirs.packages
        for name in sorted(set(b) | set(o) | set(t)):
            be, oe, te = b.get(name), o.get(name), t.get(name)
            if oe == te:
                if oe is not None:
                    self.entries[name] = oe
                    self.result.sources["packages"][name] = (
                        "both" if be != oe else "base")
                else:
                    self.entries[name] = None
                    self.deleted_by[name] = "both"
                    self.result.sources["packages"][name] = "both-deleted"
            elif be == oe and te != be:
                if te is not None:
                    self.entries[name] = te
                    self.result.sources["packages"][name] = "theirs"
                else:
                    self.entries[name] = None
                    self.deleted_by[name] = "theirs"
                    self.result.sources["packages"][name] = "theirs-deleted"
            elif be == te and oe != be:
                if oe is not None:
                    self.entries[name] = oe
                    self.result.sources["packages"][name] = "ours"
                else:
                    self.entries[name] = None
                    self.deleted_by[name] = "ours"
                    self.result.sources["packages"][name] = "ours-deleted"
            else:
                self._entry_conflict(name, be, oe, te)

    def _entry_conflict(self, name: str, be, oe, te) -> None:
        if be is not None and (oe is None or te is None):
            kind = "modify_delete_conflict"
            kept = oe or te  # the still-present, modified side wins tentatively
        elif be is None:
            kind = "both_added_conflict"
            kept = oe if oe is not None else te
        else:
            kind = "both_changed_conflict"
            kept = be  # shared dependency: keep base, closure must be re-verified
        self.entries[name] = dict(kept) if kept is not None else None
        self.result.sources["packages"][name] = (
            "conflict" if kept is not None else "conflict-deleted")
        self._conflict(kind, name, {
            "base": _ev(be), "ours": _ev(oe), "theirs": _ev(te),
            "kept": _ev(kept) if kept is not None else None,
        })

    # ------------------------------------------------------------------
    # Deletion safety
    # ------------------------------------------------------------------
    def _reject_dangling_deletions(self, roots: dict[str, str]) -> None:
        retained = {n for n, e in self.entries.items() if e is not None}
        for name, deleted_by in list(self.deleted_by.items()):
            if self.entries.get(name) is not None:
                continue  # already restored by an entry conflict
            referrers = self._referrers(name, retained, roots)
            if not referrers:
                continue
            restored = self._restore_entry(name)
            self.entries[name] = restored
            retained.add(name)
            self.result.sources["packages"][name] = "restored"
            item = {
                "type": "deletion_still_referenced",
                "package": name,
                "deleted_by": deleted_by,
                "referenced_by": referrers,
                "restored_version": restored["version"],
                "message": (f"refusing to delete {name}: still required by "
                            + ", ".join(referrers)),
            }
            self.result.rejected_deletions.append(item)

    def _referrers(self, name: str, retained: set[str],
                   roots: dict[str, str]) -> list[str]:
        referrers: set[str] = set()
        if name in roots:
            referrers.add("roots")
        for parent in sorted(retained):
            entry = self.entries[parent]
            deps = self.registry.dependencies_of(parent, entry["version"])
            if name in deps:
                referrers.add(f"{parent}@{entry['version']}")
        return sorted(referrers)

    def _restore_entry(self, name: str) -> dict:
        for lock in (self.base, self.ours, self.theirs):
            if name in lock.packages:
                return dict(lock.packages[name])
        raise AssertionError(f"cannot restore {name}: no side retained it")

    # ------------------------------------------------------------------
    # Closure verification against the local registry
    # ------------------------------------------------------------------
    def _verify_closure(self, merged: dict) -> None:
        packages = merged["packages"]
        roots = merged["roots"]

        for name, entry in packages.items():
            if not self.registry.has(name, entry["version"]):
                self._violation("unknown_package", name,
                                f"{name}@{entry['version']} is missing from the "
                                "local registry; closure cannot be verified")

        for name, ver in roots.items():
            entry = packages.get(name)
            if entry is None:
                self._violation("missing_root_package", name,
                                f"root {name}@{ver} has no locked package entry")
            elif entry["version"] != ver:
                self._violation("root_version_mismatch", name,
                                {"root_pin": ver, "locked": entry["version"]})

        for parent, entry in packages.items():
            deps = self.registry.dependencies_of(parent, entry["version"])
            for dep_name, raw in sorted(deps.items()):
                dep_entry = packages.get(dep_name)
                if dep_entry is None:
                    self._violation("missing_dependency", dep_name,
                                    {"required_by": f"{parent}@{entry['version']}",
                                     "constraint": raw,
                                     "message": f"{dep_name} ({raw}) required by "
                                                f"{parent} is not in the lock"})
                    continue
                try:
                    constraint = Constraint.parse(raw)
                    locked_ver = Version.parse(dep_entry["version"])
                except ValueError as exc:
                    self._violation("invalid_version", dep_name, str(exc))
                    continue
                if not constraint.satisfies(locked_ver):
                    self._violation("constraint_violation", dep_name, {
                        "required_by": f"{parent}@{entry['version']}",
                        "constraint": raw,
                        "locked": dep_entry["version"],
                        "message": f"locked {dep_name}@{dep_entry['version']} does "
                                   f"not satisfy {raw} required by "
                                   f"{parent}@{entry['version']}",
                    })

        self._check_reachability(packages, roots)

    def _check_reachability(self, packages: dict, roots: dict) -> None:
        reachable: set[str] = set()
        stack = [name for name in roots if name in packages]
        while stack:
            name = stack.pop()
            if name in reachable:
                continue
            reachable.add(name)
            entry = packages[name]
            for dep in self.registry.dependencies_of(name, entry["version"]):
                if dep in packages and dep not in reachable:
                    stack.append(dep)
        for name in sorted(packages):
            if name not in reachable:
                self.result.warnings.append({
                    "type": "orphan_package", "package": name,
                    "message": f"{name} is locked but unreachable from any root",
                })

    # ------------------------------------------------------------------
    def _conflict(self, kind: str, name: str, detail: dict) -> None:
        self.result.conflict_hunks.append({"type": kind, "name": name, **detail})

    def _violation(self, kind: str, name: str, detail) -> None:
        if isinstance(detail, str):
            detail = {"message": detail}
        self.result.closure_violations.append({"type": kind, "name": name, **detail})

    def _collect_unprocessable(self) -> None:
        for hunk in self.result.conflict_hunks:
            self.result.unprocessable.append({
                "type": "merge_conflict",
                "conflict": hunk["type"],
                "name": hunk["name"],
                "message": f"{hunk['type']} on {hunk['name']} requires manual "
                           "resolution and closure re-verification",
            })
        for item in self.result.rejected_deletions:
            self.result.unprocessable.append({
                "type": "deletion_rejected",
                "package": item["package"],
                "message": item["message"],
            })
        for violation in self.result.closure_violations:
            self.result.unprocessable.append({
                "type": "closure_violation",
                "violation": violation["type"],
                "name": violation["name"],
                "message": violation.get("message",
                                         f"{violation['type']} on {violation['name']}"),
            })


def _ev(entry: dict | None) -> str | None:
    return entry["version"] if entry else None
