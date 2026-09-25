"""Projection-first public explanations (the secure renderer).

Security invariant: every byte of the public output -- explanation tree,
error string, structural counts and debug log -- is computed from the
*projection* of the world (caller-visible facts) plus the public rule
set.  Hidden facts influence nothing, so two low-equivalent worlds
always produce identical public transcripts.

Consequences of the design:

* A conclusion that is visibly derivable gets a complete explanation.
* A conclusion that some public rule *could* derive, but not from
  visible facts alone, gets a canonical "potential support" skeleton in
  which every non-visible premise collapses to a single
  ``<restricted-support>`` placeholder.  The explanation is explicitly
  marked incomplete.  One placeholder stands for the whole hidden
  support, so neither path length nor any count reveals how many hidden
  facts or steps are involved.
* Anything else (hidden base fact, unknown ID) yields one generic
  error, so fact existence is not leaked either.

We explicitly do NOT promise anything about timing side channels.
"""

from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass, replace

from .cache import ExplanationCache
from .engine import Derivation, derive
from .model import Fact, Rule, World
from .projection import project, projection_digest

RESTRICTED_LABEL = "<restricted-support>"
NOT_VISIBLE_ERROR = "ERROR[not-visible]: no public explanation available"

@dataclass(frozen=True)
class PNode:
    kind: str  # 'fact' | 'step' | 'potential' | 'placeholder'
    label: str
    children: tuple["PNode", ...] = ()


_PLACEHOLDER = PNode("placeholder", RESTRICTED_LABEL)


@dataclass(frozen=True)
class Evidence:
    """Self-contained, replayable evidence for one public explanation."""

    query: str
    facts: tuple[Fact, ...]  # the exact projection the answer was built from
    rules: tuple[Rule, ...]  # the public rule set
    transcript_sha256: str


@dataclass(frozen=True)
class PublicExplanation:
    query: str
    status: str  # 'complete' | 'incomplete' | 'error'
    root: PNode | None
    counts: dict  # structural counts over the *rendered* tree only
    error: str | None
    debug: tuple[str, ...]
    evidence: Evidence | None

    def transcript(self) -> str:
        lines = [f"query: {self.query}", f"status: {self.status}"]
        if self.error is not None:
            lines.append(f"error: {self.error}")
        if self.root is not None:
            lines.append("tree:")
            lines.extend(_render_lines(self.root, 1))
        lines.append("counts: " + json.dumps(self.counts, sort_keys=True))
        lines.append("debug:")
        lines.extend(f"  {line}" for line in self.debug)
        return "\n".join(lines)


def _render_lines(node: PNode, indent: int) -> list[str]:
    lines = ["  " * indent + "- " + node.label]
    for child in node.children:
        lines.extend(_render_lines(child, indent + 1))
    return lines


def _render(derivation: Derivation) -> PNode:
    fact = derivation.fact
    if derivation.step is None:
        return PNode("fact", f'fact {fact.fact_id}@{fact.version} "{fact.payload}"')
    step = derivation.step
    label = (
        f"step {step.step_id} v{step.version}"
        f" (rule {step.rule_id}@{step.rule_version}) -> {step.conclusion}"
    )
    return PNode("step", label, tuple(_render(p) for p in derivation.premises))


def _counts(root: PNode) -> dict:
    counts = {"facts": 0, "steps": 0, "placeholders": 0, "depth": 0}

    def visit(node: PNode, depth: int) -> None:
        counts["depth"] = max(counts["depth"], depth)
        if node.kind == "fact":
            counts["facts"] += 1
        elif node.kind in ("step", "potential"):
            counts["steps"] += 1
        elif node.kind == "placeholder":
            counts["placeholders"] += 1
        for child in node.children:
            visit(child, depth + 1)

    visit(root, 1)
    return counts


def build_public_explanation(
    visible_facts: tuple[Fact, ...],
    rules: tuple[Rule, ...],
    query: str,
) -> PublicExplanation:
    """Pure function of (visible facts, public rules, query)."""
    debug: list[str] = []
    closure = derive(visible_facts, rules, debug=debug)
    if query in closure:
        root = _render(closure[query])
        return PublicExplanation(
            query=query,
            status="complete",
            root=root,
            counts=_counts(root),
            error=None,
            debug=tuple(debug),
            evidence=None,
        )
    candidates = [r for r in rules if r.conclusion == query]
    if candidates:
        # Canonical potential-support skeleton: the rule needing the
        # fewest non-visible premises, ties broken by (rule_id, version).
        # This choice depends only on the projection, never on hidden
        # facts, so it cannot leak which hidden facts actually exist.
        best = min(
            candidates,
            key=lambda r: (
                sum(1 for p in r.premises if p not in closure),
                r.rule_id,
                r.version,
            ),
        )
        children = tuple(
            _render(closure[p]) if p in closure else _PLACEHOLDER
            for p in best.premises
        )
        root = PNode(
            "potential",
            f"potential-support via rule {best.rule_id}@{best.version}"
            " (restricted premises omitted)",
            children,
        )
        debug.append("incomplete: restricted support omitted")
        return PublicExplanation(
            query=query,
            status="incomplete",
            root=root,
            counts=_counts(root),
            error=None,
            debug=tuple(debug),
            evidence=None,
        )
    return PublicExplanation(
        query=query,
        status="error",
        root=None,
        counts={},
        error=f"{NOT_VISIBLE_ERROR} for '{query}'",
        debug=tuple(debug),
        evidence=None,
    )


def explain(
    world: World,
    clearance: int,
    query: str,
    cache: ExplanationCache | None = None,
) -> PublicExplanation:
    """Project, then explain.  Optionally uses a permission-isolated cache."""
    visible = project(world, clearance)
    key = (query, projection_digest(visible, world.rules))
    if cache is not None:
        hit = cache.get(clearance, key)
        if hit is not None:
            return hit
    result = build_public_explanation(visible, world.rules, query)
    evidence = Evidence(
        query=query,
        facts=visible,
        rules=world.rules,
        transcript_sha256=hashlib.sha256(result.transcript().encode()).hexdigest(),
    )
    result = replace(result, evidence=evidence)
    if cache is not None:
        cache.put(clearance, key, result)
    return result


def replay(evidence: Evidence) -> bool:
    """Re-derive the explanation from the evidence and verify the hash."""
    rebuilt = build_public_explanation(evidence.facts, evidence.rules, evidence.query)
    digest = hashlib.sha256(rebuilt.transcript().encode()).hexdigest()
    return digest == evidence.transcript_sha256
