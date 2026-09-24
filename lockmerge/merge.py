"""Three-way merge of lock files with dependency-closure re-validation."""

from __future__ import annotations

from dataclasses import dataclass

from .lockfile import LockFile, LockedPackage

MAX_PASSES = 64


@dataclass
class Change:
    package: str
    action: str
    source: str
    detail: str

    def to_json(self):
        return {
            "package": self.package,
            "action": self.action,
            "source": self.source,
            "detail": self.detail,
        }


@dataclass
class Unresolved:
    package: str
    reason: str
    detail: str

    def to_json(self):
        return {"package": self.package, "reason": self.reason, "detail": self.detail}


@dataclass
class MergeResult:
    lock: LockFile
    provenance: dict
    changes: list
    unresolved: list

    @property
    def ok(self):
        return not self.unresolved

    def to_report(self):
        return {
            "status": "ok" if self.ok else "conflict",
            "provenance": {name: self.provenance[name] for name in sorted(self.provenance)},
            "changes": [change.to_json() for change in self.changes],
            "unresolved": [item.to_json() for item in self.unresolved],
            "merged": self.lock.to_json(),
        }


def merge_locks(base, ours, theirs, registry=None):
    """Merge *ours* and *theirs* against *base*, re-validating the dependency closure.

    *registry* is a local Registry used to re-resolve shared dependencies; no
    network access is performed. Returns a MergeResult whose ``unresolved``
    list holds every item that could not be handled automatically.
    """
    changes = []
    unresolved = []
    provenance = {}
    merged = {}
    deleted = set()

    names = set(base.packages) | set(ours.packages) | set(theirs.packages)
    for name in sorted(names):
        b = base.packages.get(name)
        o = ours.packages.get(name)
        t = theirs.packages.get(name)
        if o == t:
            if o is None:
                if b is not None:
                    deleted.add(name)
                    changes.append(
                        Change(name, "remove", "both", "removed identically by both sides")
                    )
            else:
                merged[name] = o
                if o == b:
                    provenance[name] = "base"
                else:
                    provenance[name] = "both"
                    changes.append(
                        Change(
                            name,
                            _action(b, o),
                            "both",
                            f"identical change {_ver(b)} -> {_ver(o)} kept once",
                        )
                    )
        elif _same(o, b):
            if t is None:
                deleted.add(name)
                changes.append(Change(name, "remove", "theirs", f"removed by theirs (was {_ver(b)})"))
            else:
                merged[name] = t
                provenance[name] = "theirs"
                changes.append(Change(name, _action(b, t), "theirs", f"{_ver(b)} -> {_ver(t)}"))
        elif _same(t, b):
            if o is None:
                deleted.add(name)
                changes.append(Change(name, "remove", "ours", f"removed by ours (was {_ver(b)})"))
            else:
                merged[name] = o
                provenance[name] = "ours"
                changes.append(Change(name, _action(b, o), "ours", f"{_ver(b)} -> {_ver(o)}"))
        else:
            unresolved.append(Unresolved(name, "pin-conflict", _pin_conflict_detail(name, b, o, t)))
            if b is not None:
                merged[name] = b
                provenance[name] = "base (unresolved conflict)"

    lock = LockFile(merged)
    _revalidate_closure(lock, registry, deleted, provenance, changes, unresolved)
    return MergeResult(lock, provenance, changes, _dedupe(unresolved))


def _revalidate_closure(lock, registry, deleted, provenance, changes, unresolved):
    """Fixpoint loop: check the merged closure, re-resolving shared dependencies."""
    for _ in range(MAX_PASSES):
        problems = _find_problems(lock, registry)
        if not problems:
            return
        progress = False
        for kind, name, detail in problems:
            if kind == "unknown-version":
                unresolved.append(Unresolved(name, "unknown-version", detail))
                continue
            constraints = _constraints_for(lock, name)
            candidate = None
            if registry is not None and name in registry:
                candidate = registry.resolve(name, constraints)
            if kind == "missing" and name in deleted:
                unresolved.append(
                    Unresolved(
                        name,
                        "dangling-reference",
                        f"{name} was removed but is still required: {detail}",
                    )
                )
            elif candidate is None:
                reason = "missing-unresolvable" if kind == "missing" else "shared-dependency-conflict"
                wanted = ", ".join(str(c) for c in constraints) or "<none>"
                unresolved.append(
                    Unresolved(
                        name,
                        reason,
                        f"no locally available version of {name} satisfies: {wanted}",
                    )
                )
            elif kind == "missing":
                lock.packages[name] = LockedPackage(candidate, registry.dependencies(name, candidate))
                provenance[name] = "re-resolved"
                changes.append(
                    Change(name, "add", "re-resolved", f"added {candidate} to close the dependency graph")
                )
                progress = True
            elif candidate != lock.packages[name].version:
                old = lock.packages[name].version
                lock.packages[name] = LockedPackage(candidate, registry.dependencies(name, candidate))
                provenance[name] = "re-resolved"
                changes.append(
                    Change(name, "update", "re-resolved", f"{old} -> {candidate} to satisfy merged constraints")
                )
                progress = True
            else:
                unresolved.append(
                    Unresolved(name, "shared-dependency-conflict", f"locked {candidate} cannot satisfy: {detail}")
                )
        if not progress:
            return


def _find_problems(lock, registry):
    problems = []
    for name in sorted(lock.packages):
        pkg = lock.packages[name]
        if registry is not None and name in registry and not registry.has_version(name, pkg.version):
            problems.append(
                ("unknown-version", name, f"locked version {pkg.version} of {name} is not in the local registry")
            )
        for dep, constraint in sorted(pkg.dependencies.items()):
            target = lock.packages.get(dep)
            if target is None:
                problems.append(("missing", dep, f"{name} requires {dep} {constraint}"))
            elif not constraint.allows(target.version):
                problems.append(
                    ("constraint-violation", dep, f"{name} requires {dep} {constraint} but {target.version} is locked")
                )
    return problems


def _constraints_for(lock, name):
    constraints = []
    for pkg in lock.packages.values():
        constraint = pkg.dependencies.get(name)
        if constraint is not None:
            constraints.append(constraint)
    return constraints


def _same(left, right):
    if left is None or right is None:
        return left is None and right is None
    return left == right


def _ver(pkg):
    return str(pkg.version) if pkg is not None else "<none>"


def _action(before, after):
    if before is None:
        return "add"
    if after is None:
        return "remove"
    return "update" if before.version != after.version else "modify"


def _pin_conflict_detail(name, b, o, t):
    if b is None:
        return f"both sides added {name} with different pins ({_ver(o)} vs {_ver(t)})"
    if o is None:
        return f"ours removed {name} but theirs changed it to {_ver(t)}"
    if t is None:
        return f"theirs removed {name} but ours changed it to {_ver(o)}"
    return f"both sides changed {name} differently ({_ver(b)} -> {_ver(o)} vs {_ver(t)})"


def _dedupe(items):
    seen = set()
    result = []
    for item in items:
        key = (item.package, item.reason, item.detail)
        if key not in seen:
            seen.add(key)
            result.append(item)
    return result
