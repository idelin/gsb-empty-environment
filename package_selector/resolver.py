from __future__ import annotations

from dataclasses import dataclass
import functools
from typing import Iterable

from .manifest import (
    Build,
    ConflictRule,
    Manifest,
    Package,
    Provider,
    Requirement,
)
from .versions import Constraint, Version


class SelectionError(Exception):
    pass


class NoSolution(SelectionError):
    pass


class CycleDetected(NoSolution):
    pass


@dataclass(frozen=True, slots=True)
class Selection:
    build: str
    target: str
    capabilities: dict[str, str]
    implementations: dict[str, str]
    selected_packages: dict[str, str]
    tie_breaker: tuple[str, ...]


TIE_BREAKER = (
    "candidate must satisfy every requested version constraint",
    "candidate must match the build target",
    "exclude prereleases unless that capability explicitly requests/allows one",
    "prefer the higher capability semantic version",
    "prefer the higher package semantic version",
    "prefer the lexicographically smaller package name",
    "prefer the lexicographically smaller capability declaration",
)


@functools.total_ordering
class _Reverse:
    __slots__ = ("value",)

    def __init__(self, value: Version) -> None:
        self.value = value

    def __lt__(self, other: "_Reverse") -> bool:
        return self.value > other.value

    def __eq__(self, other: object) -> bool:
        return isinstance(other, _Reverse) and self.value == other.value

    def __gt__(self, other: "_Reverse") -> bool:
        return self.value < other.value


@dataclass(slots=True)
class _State:
    selected_packages: dict[str, Package]
    capability_provider: dict[str, Provider]

    def snapshot(self) -> tuple[dict[str, Package], dict[str, Provider]]:
        return (dict(self.selected_packages), dict(self.capability_provider))

    def restore(self, snapshot: tuple[dict[str, Package], dict[str, Provider]]) -> None:
        self.selected_packages, self.capability_provider = snapshot


class Resolver:
    def __init__(self, manifest: Manifest, build: Build) -> None:
        self.manifest = manifest
        self.build = build
        self.state = _State({}, {})
        self.providers: dict[str, list[Provider]] = {}
        for package in manifest.packages:
            for provider in package.providers:
                self.providers.setdefault(provider.capability, []).append(provider)
        self.package_by_name_version = {
            (package.name, package.version): package for package in manifest.packages
        }

    def resolve(self) -> Selection:
        try:
            self._satisfy_all(self.build.requirements, frozenset(), frozenset())
        except RecursionError as exc:
            raise CycleDetected("alternative chain is too deep; likely cyclic alternatives") from exc
        self._finish()
        return Selection(
            build=self.build.name,
            target=self.build.target,
            capabilities={
                capability: provider.package
                for capability, provider in sorted(self.state.capability_provider.items())
            },
            implementations={
                capability: f"{provider.package}@{provider.package_version}"
                for capability, provider in sorted(self.state.capability_provider.items())
            },
            selected_packages={
                name: str(package.version)
                for name, package in sorted(self.state.selected_packages.items())
            },
            tie_breaker=TIE_BREAKER,
        )

    def _finish(self) -> None:
        for provider in self.state.capability_provider.values():
            self._check_conflicts_for(self.package_by_name_version[
                (provider.package, provider.package_version)])

    def _prerelease_allowed(self, capability: str, constraints: Iterable[Constraint]) -> bool:
        explicit = "*" in self.build.allow_prereleases or capability in self.build.allow_prereleases
        return explicit or any(constraint.explicitly_mentions_prerelease for constraint in constraints)

    def _target_matches(self, targets: frozenset[str]) -> bool:
        return not self.build.target or not targets or self.build.target in targets

    def _eligible(self, provider: Provider, requirement: Requirement) -> bool:
        if not all(constraint.allows(provider.version) for constraint in requirement.constraints):
            return False
        if not self._target_matches(provider.targets):
            return False
        package = self.package_by_name_version[(provider.package, provider.package_version)]
        if package.targets and not self._target_matches(package.targets):
            return False
        if (provider.version.is_prerelease or package.version.is_prerelease) and not self._prerelease_allowed(
                requirement.capability, requirement.constraints):
            return False
        return True

    @staticmethod
    def _candidate_key(provider: Provider) -> tuple[_Reverse, _Reverse, str, str]:
        return (
            _Reverse(provider.version),
            _Reverse(provider.package_version),
            provider.package,
            provider.capability,
        )

    def _ordered_candidates(self, requirement: Requirement) -> list[Provider]:
        candidates = self.providers.get(requirement.capability, [])
        eligible = [provider for provider in candidates if self._eligible(provider, requirement)]
        return sorted(eligible, key=self._candidate_key)

    def _existing_satisfies(self, requirement: Requirement) -> bool:
        provider = self.state.capability_provider.get(requirement.capability)
        return provider is not None and self._eligible(provider, requirement)

    def _conflict_rule_matches_version(self, actual: Version,
                                       constraints: Iterable[Constraint]) -> bool:
        return all(constraint.allows(actual) for constraint in constraints)

    def _check_rule_against_selected(self, rule: ConflictRule, source: Package) -> None:
        if rule.package:
            package = self.state.selected_packages.get(rule.package)
            if package and package.name != source.name and self._conflict_rule_matches_version(
                    package.version, rule.constraints):
                raise NoSolution(
                    f"package {source.name}@{source.version} conflicts with "
                    f"{package.name}@{package.version}"
                )
        if rule.capability:
            provider = self.state.capability_provider.get(rule.capability)
            if provider and provider.package != source.name and self._conflict_rule_matches_version(
                    provider.version, rule.constraints):
                raise NoSolution(
                    f"package {source.name}@{source.version} conflicts with capability "
                    f"{provider.capability} from {provider.package}@{provider.package_version}"
                )

    def _check_conflicts_for(self, source: Package) -> None:
        for rule in source.conflicts:
            self._check_rule_against_selected(rule, source)
        for package in self.state.selected_packages.values():
            if package.name == source.name:
                continue
            for rule in package.conflicts:
                self._check_reverse_rule(rule, source, package)

    def _check_reverse_rule(self, rule: ConflictRule, candidate: Package,
                           rule_owner: Package) -> None:
        if rule.package == candidate.name and self._conflict_rule_matches_version(
                candidate.version, rule.constraints):
            raise NoSolution(
                f"package {rule_owner.name}@{rule_owner.version} conflicts with "
                f"{candidate.name}@{candidate.version}"
            )
        if not rule.capability:
            return
        for provider in candidate.providers:
            if (self._target_matches(provider.targets)
                    and not candidate.version.is_prerelease
                    and not provider.version.is_prerelease
                    and provider.capability == rule.capability
                    and self._conflict_rule_matches_version(provider.version, rule.constraints)):
                raise NoSolution(
                    f"package {candidate.name}@{candidate.version} provides conflicting "
                    f"capability {provider.capability} for {rule_owner.name}@{rule_owner.version}"
                )

    def _add_package(self, provider: Provider) -> tuple[Provider, ...]:
        package = self.package_by_name_version[(provider.package, provider.package_version)]
        existing = self.state.selected_packages.get(package.name)
        if existing is not None:
            if existing.version != package.version:
                raise NoSolution(
                    f"single package {package.name} cannot have two versions: "
                    f"{existing.version} and {package.version}"
                )
            owner = self.state.capability_provider.get(provider.capability)
            if owner is not None and owner.package != package.name:
                raise NoSolution(
                    f"capability {provider.capability} already has a single provider: "
                    f"{owner.package}; cannot also select {package.name}"
                )
            if owner is None:
                self.state.capability_provider[provider.capability] = provider
            return ()

        self._check_conflicts_for(package)
        automatically_provided = tuple(
            offered for offered in package.providers
            if self._target_matches(offered.targets)
        )
        claims = {provider.capability: provider}
        claims.update({offered.capability: offered for offered in automatically_provided})
        for offered in package.providers:
            claimed = claims.get(offered.capability)
            if claimed is None or claimed.package != package.name:
                continue
            owner = self.state.capability_provider.get(offered.capability)
            if owner is not None and owner.package != package.name:
                raise NoSolution(
                    f"capability {offered.capability} already has a single provider: "
                    f"{owner.package}; cannot also select {package.name}"
                )

        self.state.selected_packages[package.name] = package
        added_providers: list[Provider] = []
        for offered in claims.values():
            owner = self.state.capability_provider.get(offered.capability)
            if owner is None:
                self.state.capability_provider[offered.capability] = offered
                added_providers.append(offered)
            elif owner.package == package.name:
                existing_package = self.package_by_name_version[(owner.package, owner.package_version)]
                if existing_package.version != package.version:
                    raise NoSolution(f"inconsistent selected version for {package.name}")
        return tuple(added_providers)

    def _satisfy_all(self, requirements: tuple[Requirement, ...],
                     active_plain: frozenset[str],
                     active_alternatives: frozenset[str]) -> None:
        if not requirements:
            return
        requirement, remaining = requirements[0], requirements[1:]
        if requirement.is_alternative_group:
            self._satisfy_alternatives(requirement, remaining, active_plain,
                                       active_alternatives)
            return

        if self._existing_satisfies(requirement):
            self._satisfy_all(remaining, active_plain, active_alternatives)
            return

        capability = requirement.capability
        if capability in active_plain:
            raise CycleDetected(
                f"cyclic requirement detected while resolving capability {capability!r}"
            )
        candidates = self._ordered_candidates(requirement)
        if not candidates:
            raise NoSolution(
                f"no eligible implementation provides capability {capability!r}"
            )

        failures: list[str] = []
        next_plain = active_plain | {capability}
        for provider in candidates:
            snapshot = self.state.snapshot()
            try:
                self._add_package(provider)
                package = self.package_by_name_version[
                    (provider.package, provider.package_version)
                ]
                self._satisfy_all(package.requires + remaining, next_plain,
                                  active_alternatives)
                return
            except NoSolution as exc:
                failures.append(str(exc))
                self.state.restore(snapshot)
        detail = "; ".join(dict.fromkeys(failures))
        raise NoSolution(
            f"no eligible implementation provides capability {capability!r}: {detail}"
        )

    def _requirement_order(self, requirement: Requirement) -> tuple[object, ...]:
        return (
            requirement.capability,
            tuple((constraint.operator,
                   str(constraint.version) if constraint.version else constraint.raw)
                  for constraint in requirement.constraints),
        )

    def _best_option_provider(self, requirement: Requirement) -> Provider | None:
        candidates = [
            provider for provider in self.providers.get(requirement.capability, [])
            if self._eligible(provider, requirement)
        ]
        return min(candidates, key=self._candidate_key) if candidates else None

    def _option_rank(
        self, option: tuple[Requirement, ...]
    ) -> tuple[tuple, tuple[tuple[object, ...], ...]] | None:
        rank: list[tuple[_Reverse, _Reverse, str, str]] = []
        for requirement in option:
            if requirement.is_alternative_group:
                return None
            provider = self._best_option_provider(requirement)
            if provider is None:
                return None
            rank.append(self._candidate_key(provider))
        requirement_order = tuple(self._requirement_order(requirement) for requirement in option)
        return tuple(rank), requirement_order

    def _satisfy_alternatives(self, group: Requirement, remaining: tuple[Requirement, ...],
                              active_plain: frozenset[str],
                              active_alternatives: frozenset[str]) -> None:
        ranked_options: list[tuple[tuple, tuple[Requirement, ...]]] = []
        for option in group.alternatives:
            rank = self._option_rank(option)
            if rank is not None:
                ranked_options.append((rank, option))
        ranked_options.sort(key=lambda item: item[0])

        failures: list[str] = []
        for _, option in ranked_options:
            snapshot = self.state.snapshot()
            try:
                option_capabilities = frozenset(
                    requirement.capability
                    for requirement in option
                    if not requirement.is_alternative_group
                )
                if option_capabilities & active_alternatives:
                    cyclic_capability = sorted(option_capabilities & active_alternatives)[0]
                    raise CycleDetected(
                        "cyclic alternative chain detected for capability "
                        f"{cyclic_capability!r}"
                    )
                self._satisfy_all(tuple(option) + remaining, active_plain,
                                  active_alternatives | option_capabilities)
                return
            except NoSolution as exc:
                failures.append(str(exc))
                self.state.restore(snapshot)

        if ranked_options:
            detail = "; ".join(dict.fromkeys(failures))
        else:
            detail = "every alternative is unavailable for the requested version/target"
        raise NoSolution(f"no alternative group could be satisfied: {detail}")


def resolve(manifest: Manifest, build_name: str | None = None) -> Selection:
    if not manifest.builds:
        raise SelectionError("manifest contains no build targets")
    if build_name is None:
        if len(manifest.builds) != 1:
            names = [build.name for build in manifest.builds]
            raise SelectionError(f"multiple builds found; choose one of: {', '.join(names)}")
        build = manifest.builds[0]
    else:
        matches = [build for build in manifest.builds if build.name == build_name]
        if not matches:
            raise SelectionError(f"unknown build: {build_name!r}")
        if len(matches) > 1:
            raise SelectionError(f"duplicate build name: {build_name!r}")
        build = matches[0]
    return Resolver(manifest, build).resolve()
