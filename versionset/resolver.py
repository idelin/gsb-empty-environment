"""Backtracking resolver over a local repo, with constraint provenance.

Every constraint remembers its source (the root requirement or the exact
package version that introduced it).  The resolver records, for each
selected package, which constraints admitted the chosen version and which
constraints excluded the other candidates, so a lock change can be
explained in terms of the constraints that forced a different selection.
Unsatisfiable inputs produce a structured conflict plus a human readable
explanation instead of a bare failure.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .repo import BuildInput, Repo
from .sets import VersionSet
from .version import Version


@dataclass(frozen=True)
class Constraint:
    source: str          # "root" or e.g. "lib@1.2.0"
    package: str         # package the constraint applies to
    spec: VersionSet

    def describe(self) -> str:
        return f"{self.source} requires {self.package} {self.spec.canonical()}"


@dataclass
class Conflict(Exception):
    package: str
    constraints: list[Constraint]
    available: list[Version]
    detail: str = ""


class Unsatisfiable(Exception):
    def __init__(self, conflict: Conflict):
        self.conflict = conflict
        super().__init__(self.explanation())

    def explanation(self) -> str:
        c = self.conflict
        lines = [f"cannot select a version of '{c.package}':"]
        for con in c.constraints:
            lines.append(f"  - {con.describe()}")
        combined = VersionSet.full()
        for con in c.constraints:
            combined = combined.intersect(con.spec)
        if combined.is_empty():
            lines.append("  => the constraints are mutually exclusive "
                         "(empty intersection)")
        else:
            lines.append(f"  => combined constraint {combined.canonical()}")
        avail = ", ".join(str(v) for v in c.available) or "(none)"
        lines.append(f"  available versions in repo: {avail}")
        if c.detail:
            lines.append(f"  note: {c.detail}")
        return "\n".join(lines)


@dataclass
class SelectionInfo:
    chosen: Version
    constraints: list[Constraint] = field(default_factory=list)
    rejected: list[tuple[Version, str]] = field(default_factory=list)


@dataclass
class Resolution:
    assignment: dict[str, Version]
    reasons: dict[str, SelectionInfo]

    def version_of(self, name: str) -> Version | None:
        return self.assignment.get(name)


class Resolver:
    def __init__(self, repo: Repo, target: str | None = None):
        self.repo = repo
        self.target = target

    # -- public API -----------------------------------------------------
    def resolve(self, requirements: dict[str, VersionSet],
                lock: dict[str, Version] | None = None) -> Resolution:
        constraints = self._root_constraints(requirements)
        assignment: dict[str, Version] = {}
        reasons: dict[str, SelectionInfo] = {}
        try:
            self._search(constraints, assignment, lock or {}, reasons,
                         collect_all=None)
        except Conflict as exc:
            raise Unsatisfiable(exc) from exc
        return Resolution(assignment, reasons)

    def resolve_build(self, build: BuildInput) -> Resolution:
        return self.resolve(build.requirements, build.lock)

    def iter_solutions(self, requirements: dict[str, VersionSet],
                       limit: int = 1000):
        """Enumerate solutions (used to cross-validate against brute force)."""
        constraints = self._root_constraints(requirements)
        solutions: list[dict] = []
        self._search(constraints, {}, {}, {}, collect_all=solutions,
                     limit=limit)
        return solutions

    def explain_lock_change(self, requirements: dict[str, VersionSet],
                            old_lock: dict[str, Version],
                            new_lock: dict[str, Version]) -> dict:
        """Resolve under two locks and attribute every selection change
        to the constraints that caused it."""
        old = self.resolve(requirements, old_lock)
        new = self.resolve(requirements, new_lock)
        changes = {}
        for name in sorted(set(old.assignment) | set(new.assignment)):
            ov, nv = old.assignment.get(name), new.assignment.get(name)
            if ov == nv:
                continue
            causes = []
            info = new.reasons.get(name)
            if info is not None and ov is not None:
                for con in info.constraints:
                    if not con.spec.contains(ov, self.target):
                        causes.append(
                            f"{con.describe()} excludes previously "
                            f"selected {ov}")
            if not causes and info is not None:
                causes = [c.describe() for c in info.constraints]
            changes[name] = {"old": str(ov) if ov else None,
                             "new": str(nv) if nv else None,
                             "caused_by": causes}
        return changes

    # -- search -----------------------------------------------------------
    def _root_constraints(self, requirements):
        return {name: [Constraint("root", name, spec)]
                for name, spec in requirements.items()}

    def _candidates(self, name, cons):
        out = []
        for pv in self.repo.versions(name):
            if all(c.spec.contains(pv.version, self.target) for c in cons):
                out.append(pv.version)
        return sorted(out, reverse=True)

    def _search(self, constraints, assignment, lock, reasons,
                collect_all, limit=1):
        if collect_all is not None and len(collect_all) >= limit:
            return False
        pending = [n for n in constraints if n not in assignment]
        if not pending:
            if collect_all is not None:
                collect_all.append(dict(assignment))
                return False
            return True
        # MRV: branch on the package with the fewest valid candidates.
        name = min(pending,
                   key=lambda n: len(self._candidates(n, constraints[n])))
        cons = constraints[name]
        cands = self._candidates(name, cons)
        if not cands:
            raise Conflict(name, list(cons),
                           [pv.version for pv in self.repo.versions(name)])
        ordered = ([lock[name]] + [v for v in cands if v != lock[name]]
                   if name in lock and lock[name] in cands else cands)
        last_conflict = None
        for version in ordered:
            pv = self.repo.get(name, version)
            added: dict[str, list[Constraint]] = {}
            ok = True
            for dep in pv.deps if pv else ():
                if not dep.applies_to(self.target):
                    continue
                con = Constraint(f"{name}@{version}", dep.name, dep.spec)
                if dep.name in assignment:
                    if not dep.spec.contains(assignment[dep.name],
                                             self.target):
                        last_conflict = Conflict(
                            dep.name,
                            list(constraints.get(dep.name, [])) + [con],
                            [assignment[dep.name]],
                            detail=f"already selected {dep.name}@"
                                   f"{assignment[dep.name]} violates it")
                        ok = False
                        break
                    continue
                added.setdefault(dep.name, []).append(con)
            if not ok:
                continue
            for dep_name, new_cons in added.items():
                constraints.setdefault(dep_name, []).extend(new_cons)
            assignment[name] = version
            solved = False
            try:
                solved = self._search(constraints, assignment, lock, reasons,
                                      collect_all, limit)
            except Conflict as exc:
                last_conflict = exc
            if solved and collect_all is None:
                # Keep the winning assignment and constraints in place.
                self._record_reason(name, version, cons, reasons)
                return True
            for dep_name, new_cons in added.items():
                del constraints[dep_name][-len(new_cons):]
                if not constraints[dep_name]:
                    del constraints[dep_name]
            del assignment[name]
        if collect_all is not None:
            return False
        raise last_conflict or Conflict(
            name, list(cons),
            [pv.version for pv in self.repo.versions(name)],
            detail="every candidate led to a conflict downstream")

    def _record_reason(self, name, version, cons, reasons):
        info = SelectionInfo(chosen=version, constraints=list(cons))
        for pv in self.repo.versions(name):
            if pv.version == version:
                continue
            for con in cons:
                if not con.spec.contains(pv.version, self.target):
                    info.rejected.append((pv.version, con.describe()))
                    break
        reasons[name] = info
