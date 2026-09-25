"""Finite-domain pairwise noninterference tester.

Low-visibility input equivalence: two complete worlds are low-equivalent
at a clearance iff their projections (caller-visible facts) are
identical -- i.e. they differ only in hidden parts.

The tester enumerates a finite family of worlds, groups them by
projection, and compares the full public transcript (explanation, error,
structural counts, debug log) of every pair within each group, for every
query.  Any difference is a noninterference violation.

Scope note: this checks output equivalence only.  We do not promise
anything about timing side channels.
"""

from __future__ import annotations

from dataclasses import dataclass

from .model import World
from .projection import project, projection_digest


@dataclass(frozen=True)
class Violation:
    world_a: World
    world_b: World
    query: str
    transcript_a: str
    transcript_b: str


def public_transcript(render, world: World, clearance: int, query: str) -> str:
    return render(world, clearance, query).transcript()


def low_equivalent(world_a: World, world_b: World, clearance: int) -> bool:
    digest_a = projection_digest(project(world_a, clearance), world_a.rules)
    digest_b = projection_digest(project(world_b, clearance), world_b.rules)
    return digest_a == digest_b


def find_violations(render, worlds, clearance: int, queries) -> list[Violation]:
    groups: dict[str, list[World]] = {}
    for world in worlds:
        key = projection_digest(project(world, clearance), world.rules)
        groups.setdefault(key, []).append(world)
    violations: list[Violation] = []
    for key in sorted(groups):
        group = groups[key]
        reference = group[0]
        for other in group[1:]:
            for query in queries:
                transcript_ref = public_transcript(render, reference, clearance, query)
                transcript_other = public_transcript(render, other, clearance, query)
                if transcript_ref != transcript_other:
                    violations.append(
                        Violation(reference, other, query, transcript_ref, transcript_other)
                    )
    return violations
