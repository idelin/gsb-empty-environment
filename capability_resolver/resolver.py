"""Backtracking capability-to-implementation resolver.

Guarantees:
* exactly one provider is chosen for every requested capability;
* mutually exclusive packages are never selected together, and when a
  preferred provider conflicts, the next alternative provider is tried;
* target requires/excludes constraints are honored;
* ``replaced_by`` alternative chains are followed without unbounded
  recursion (a cycle is reported as an error rather than looping);
* selection is deterministic: ties are broken by documented, stable rules.
"""
from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

from .errors import ResolutionError
from .models import Catalog, Package, Requirement, Target
from .version import Constraint, Version, satisfies_all

# Stable decision rules, applied in order, and echoed in the JSON output.
DECISION_POLICY: Tuple[str, ...] = (
    "1. Drop candidates violating a target requires/excludes constraint.",
    "2. Drop candidates whose version fails any requested version constraint.",
    "3. Follow replaced_by chains; revisiting a package is a cycle error.",
    "4. Prefer the highest semantic version (a release outranks a same-core pre-release).",
    "5. Skip providers conflicting with a selected package and try the next.",
    "6. On equal version, prefer the lexicographically smallest provider name.",
    "7. On equal provider and version, keep catalog declaration order.",
)


@dataclass(frozen=True)
class Resolution:
    """The capability->implementation mapping plus the full package set."""

    mapping: Dict[str, Package]
    selected: Tuple[Package, ...]
    target: Target
    policy: Tuple[str, ...] = DECISION_POLICY

    def to_dict(self) -> dict:
        return {
            "mapping": {
                capability: {
                    "package": package.name,
                    "version": str(package.version),
                }
                for capability, package in sorted(self.mapping.items())
            },
            "selected_packages": [
                {"package": package.name, "version": str(package.version)}
                for package in sorted(self.selected, key=lambda p: (p.name, p.version))
            ],
            "target": self.target.to_dict(),
            "decision_policy": list(self.policy),
        }


class Resolver:
    def __init__(self, catalog: Catalog, target: Optional[Target] = None) -> None:
        self._catalog = catalog
        self._target = target or Target(())
        self._alt_graph = self._build_alternative_graph(catalog)
        self._routes_cache: Dict[str, Tuple[str, ...]] = {}

    @staticmethod
    def _build_alternative_graph(catalog: Catalog) -> Dict[str, str]:
        graph: Dict[str, str] = {}
        for package in catalog.packages:
            if package.replaced_by is not None:
                graph[package.name] = package.replaced_by
        return graph

    def resolve(self, requirements: Tuple[Requirement, ...]) -> Resolution:
        grouped = self._group_requirements(requirements)
        capabilities = tuple(sorted(grouped))

        mapping: Dict[str, Package] = {}
        selected: Dict[str, Package] = {}
        if not self._backtrack(capabilities, 0, grouped, mapping, selected):
            raise ResolutionError(
                self._explain_failure(capabilities, grouped, selected)
            )
        return Resolution(
            mapping=dict(mapping),
            selected=tuple(sorted(selected.values(), key=lambda p: (p.name, p.version))),
            target=self._target,
        )

    @staticmethod
    def _group_requirements(
        requirements: Tuple[Requirement, ...],
    ) -> Dict[str, List[Requirement]]:
        grouped: defaultdict[str, List[Requirement]] = defaultdict(list)
        for requirement in requirements:
            grouped[requirement.capability].append(requirement)
        return dict(grouped)

    def _routes(self, capability: str) -> Tuple[str, ...]:
        """Provider package names for a capability, expanded via alternatives.

        Alternative inheritance: when provider P is replaced by Q, Q may supply
        the capability without redeclaring it. Cycles raise immediately so a
        ring of ``replaced_by`` links can never recurse forever.
        """
        if capability in self._routes_cache:
            return self._routes_cache[capability]

        result: List[str] = []
        seen: set = set()
        visiting: List[str] = []

        def expand(name: str) -> None:
            if name in visiting:
                cycle = " -> ".join(visiting[visiting.index(name):] + [name])
                raise ResolutionError(f"alternative chain forms a cycle: {cycle}")
            if name in seen:
                return
            visiting.append(name)
            seen.add(name)
            result.append(name)
            successor = self._alt_graph.get(name)
            if successor is not None:
                expand(successor)
            visiting.pop()

        direct = sorted(
            {package.name
             for package in self._catalog.capability_index.get(capability, ())}
        )
        for name in direct:
            expand(name)

        self._routes_cache[capability] = tuple(result)
        return self._routes_cache[capability]

    def _versions_for(
        self, capability: str, package_name: str, constraints: Tuple[Constraint, ...]
    ) -> List[Package]:
        matches: List[Package] = []
        direct_names = {
            package.name
            for package in self._catalog.capability_index.get(capability, ())
        }
        for package in self._catalog.by_name.get(package_name, ()):
            # A direct provider must declare the capability. A package reached
            # only through a replaced_by chain inherits the capability.
            if capability not in package.provides and package_name in direct_names:
                continue
            if not package.matches_target(self._target):
                continue
            if not satisfies_all(package.version, constraints):
                continue
            matches.append(package)
        # Highest version first within this provider (rule 4).
        matches.sort(key=lambda p: p.version, reverse=True)
        return matches

    def _ordered_candidates(
        self, capability: str, constraints: Tuple[Constraint, ...]
    ) -> List[Package]:
        groups: Dict[str, List[Package]] = {}
        for package_name in self._routes(capability):
            versions = self._versions_for(capability, package_name, constraints)
            if versions:
                groups[package_name] = versions

        # Providers are tried best-version-first; ties break by name asc (rule 6).
        provider_names = sorted(
            groups, key=lambda name: (self._negate(groups[name][0].version), name)
        )
        ordered: List[Package] = []
        for name in provider_names:
            ordered.extend(groups[name])
        return ordered

    @staticmethod
    def _negate(version: Version):
        # Return an order key that sorts higher versions first.
        return _Desc(version)

    def _constraints_for(
        self, capability: str, grouped: Dict[str, List[Requirement]]
    ) -> Tuple[Constraint, ...]:
        return tuple(
            constraint
            for requirement in grouped[capability]
            for constraint in requirement.constraints
        )

    def _backtrack(
        self,
        capabilities: Tuple[str, ...],
        index: int,
        grouped,
        mapping: Dict[str, Package],
        selected: Dict[str, Package],
    ) -> bool:
        if index == len(capabilities):
            return True

        capability = capabilities[index]
        constraints = self._constraints_for(capability, grouped)
        candidates = self._ordered_candidates(capability, constraints)

        for candidate in candidates:
            prior = selected.get(candidate.name)
            if prior is not None:
                # Same package already selected: reuse only the identical version.
                if prior.version != candidate.version:
                    continue
                mapping[capability] = prior
                if self._backtrack(
                    capabilities, index + 1, grouped, mapping, selected
                ):
                    return True
                mapping.pop(capability, None)
                continue

            if self._conflicts(candidate, selected):
                continue

            selected[candidate.name] = candidate
            mapping[capability] = candidate
            if self._backtrack(
                capabilities, index + 1, grouped, mapping, selected
            ):
                return True
            mapping.pop(capability, None)
            selected.pop(candidate.name, None)

        return False

    @staticmethod
    def _conflicts(candidate: Package, selected: Dict[str, Package]) -> bool:
        for other in selected.values():
            if other.name in candidate.conflicts_with:
                return True
            if candidate.name in other.conflicts_with:
                return True
        return False

    def _explain_failure(
        self, capabilities, grouped, selected: Dict[str, Package]
    ) -> str:
        problems = []
        for capability in capabilities:
            constraints = self._constraints_for(capability, grouped)
            candidates = self._ordered_candidates(capability, constraints)
            if not candidates:
                problems.append(
                    f"{capability!r}: no version satisfies the target/constraints"
                )
                continue
            blocked = []
            for candidate in candidates:
                clashes = sorted(
                    other.name
                    for other in selected.values()
                    if other.name in candidate.conflicts_with
                    or candidate.name in other.conflicts_with
                )
                if clashes:
                    blocked.append(f"{candidate.name}@{candidate.version} (conflicts {clashes})")
            if len(blocked) == len(candidates):
                problems.append(
                    f"{capability!r}: every provider conflicts -> {', '.join(blocked)}"
                )
        if not problems:
            problems.append("providers are mutually exclusive across the requirements")
        return "cannot resolve: " + "; ".join(problems)


class _Desc:
    """Wrapper that reverses Version ordering for higher-version-first sorts."""

    __slots__ = ("version",)

    def __init__(self, version: Version) -> None:
        self.version = version

    def __lt__(self, other: "_Desc") -> bool:
        return self.version > other.version

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _Desc) and self.version == other.version
