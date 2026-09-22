"""Human/machine readable explanations of unsatisfiable constraints."""
from __future__ import annotations

from dataclasses import dataclass

from .solver import Unsatisfiable


@dataclass(frozen=True)
class Explanation:
    package: str
    constraint_sets: tuple[str, ...]
    paths: tuple[tuple[str, ...], ...]
    known_versions: tuple[str, ...]
    hint: str

    def to_dict(self) -> dict:
        return {
            "package": self.package,
            "constraint_sets": list(self.constraint_sets),
            "paths": [list(path) for path in self.paths],
            "known_versions": list(self.known_versions),
            "hint": self.hint,
        }

    def render(self) -> str:
        lines = [f"cannot satisfy {self.package}:"]
        for spec_set, path in zip(self.constraint_sets, self.paths):
            lines.append(f"  - {spec_set}  [{' -> '.join(path)}]")
        lines.append(f"  known versions: {', '.join(self.known_versions) or 'none'}")
        lines.append(f"  hint: {self.hint}")
        return "\n".join(lines)


def explain(error: Unsatisfiable) -> Explanation:
    core = error.core
    allowed = set()
    for requirement in core.requirements:
        allowed |= requirement.set.allowed_channels
    prereleases = [v for v in core.available if v.is_prerelease]
    wanted_channels = {v.channel for v in prereleases}
    hint = "intersect the listed sets; their shared candidate set is empty"
    if wanted_channels and not (allowed & wanted_channels):
        hint = (
            f"matching versions are prereleases on channels "
            f"{sorted(wanted_channels)} but no constraint allows those channels"
        )
    return Explanation(
        core.package,
        tuple(req.set.serialize() for req in core.requirements),
        tuple(req.origin for req in core.requirements),
        tuple(str(v) for v in core.available),
        hint,
    )
