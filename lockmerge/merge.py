"""Three-way merge of lock files with closure re-validation.

Merge rules
-----------
* A section entry (root / package / build input) changed on only one side is
  taken from that side ("independent change, auto-merged").
* An entry changed identically on both sides is kept once.
* An entry changed differently on both sides, or deleted on one side and
  modified on the other, is a conflict and is never silently concatenated.
* After the per-entry merge, the dependency closure is re-validated: every
  root and every package dependency constraint must be satisfied by a package
  present in the merged lock. Violations (e.g. both sides upgraded different
  roots and the shared package constraint no longer holds, or a deleted
  package is still referenced) are reported as unresolvable items instead of
  emitting a broken lock.

Every merged entry carries provenance: "base", "ours", "theirs", or "both".
"""

from __future__ import annotations

from dataclasses import dataclass, field

from .lockfile import Lockfile
from .versions import Constraint, Version

SOURCE_BASE = "base"
SOURCE_OURS = "ours"
SOURCE_THEIRS = "theirs"
SOURCE_BOTH = "both"


@dataclass
class MergeResult:
    lock: Lockfile
    provenance: dict = field(default_factory=dict)   # section -> {name: source}
    conflicts: list = field(default_factory=list)    # per-entry three-way conflicts
    unresolvable: list = field(default_factory=list)  # closure validation failures

    @property
    def clean(self) -> bool:
        return not self.conflicts and not self.unresolvable

    def to_dict(self) -> dict:
        return {
            "status": "clean" if self.clean else "needs-manual-resolution",
            "lock": self.lock.to_dict(),
            "provenance": self.provenance,
            "conflicts": self.conflicts,
            "unresolvable": self.unresolvable,
        }


def _merge_section(base: dict, ours: dict, theirs: dict, section: str,
                   merged: dict, provenance: dict, conflicts: list) -> None:
    """Generic three-way merge over a name -> value mapping."""
    prov = provenance.setdefault(section, {})
    for name in sorted(set(base) | set(ours) | set(theirs)):
        b, o, t = base.get(name), ours.get(name), theirs.get(name)
        if o == t:
            if o is not None:
                merged[name] = o
                prov[name] = SOURCE_BOTH if o != b else SOURCE_BASE
            # deleted on both sides (or never existed): stays deleted
        elif o == b:
            # changed only by theirs (or deleted by theirs)
            if t is not None:
                merged[name] = t
                prov[name] = SOURCE_THEIRS
        elif t == b:
            # changed only by ours (or deleted by ours)
            if o is not None:
                merged[name] = o
                prov[name] = SOURCE_OURS
        else:
            kind = "delete-vs-modify" if (o is None or t is None) else "modify-vs-modify"
            conflicts.append({
                "section": section,
                "name": name,
                "kind": kind,
                "base": b,
                "ours": o,
                "theirs": t,
                "reason": "both sides changed the entry differently; manual resolution required",
            })


def _validate_closure(lock: Lockfile, base: Lockfile, result: MergeResult) -> None:
    """Re-validate the dependency closure of the merged lock."""
    deleted = set(base.packages) - set(lock.packages)

    def check(owner_section: str, owner: str, dep_name: str, constraint_text: str) -> None:
        constraint = Constraint(constraint_text)
        target = lock.packages.get(dep_name)
        if target is None:
            if dep_name in deleted:
                result.unresolvable.append({
                    "kind": "deleted-but-referenced",
                    "package": dep_name,
                    "referenced_by": f"{owner_section}:{owner}",
                    "constraint": constraint_text,
                    "reason": f"package {dep_name!r} was deleted but is still referenced by "
                              f"{owner_section} entry {owner!r}; deletion rejected",
                })
            else:
                result.unresolvable.append({
                    "kind": "missing-dependency",
                    "package": dep_name,
                    "referenced_by": f"{owner_section}:{owner}",
                    "constraint": constraint_text,
                    "reason": f"no package named {dep_name!r} in the merged lock",
                })
            return
        version = Version.parse(target["version"])
        if not constraint.allows(version):
            result.unresolvable.append({
                "kind": "constraint-violation",
                "package": dep_name,
                "referenced_by": f"{owner_section}:{owner}",
                "constraint": constraint_text,
                "locked_version": target["version"],
                "reason": f"merged lock has {dep_name} {target['version']} which does not "
                          f"satisfy {constraint_text!r} required by {owner_section} entry "
                          f"{owner!r}; closure must be re-resolved",
            })

    for name, constraint_text in sorted(lock.roots.items()):
        check("roots", name, name, constraint_text)
    for name, rec in sorted(lock.packages.items()):
        for dep_name, dep_constraint in sorted(rec.get("dependencies", {}).items()):
            check("packages", name, dep_name, dep_constraint)


def merge_lockfiles(base: Lockfile, ours: Lockfile, theirs: Lockfile) -> MergeResult:
    result = MergeResult(lock=Lockfile())
    _merge_section(base.roots, ours.roots, theirs.roots,
                   "roots", result.lock.roots, result.provenance, result.conflicts)
    _merge_section(base.packages, ours.packages, theirs.packages,
                   "packages", result.lock.packages, result.provenance, result.conflicts)
    _merge_section(base.build_inputs, ours.build_inputs, theirs.build_inputs,
                   "build_inputs", result.lock.build_inputs, result.provenance, result.conflicts)
    _validate_closure(result.lock, base, result)
    return result
