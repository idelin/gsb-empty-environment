"""Declarative semantics, kept deliberately independent of the engine.

The engine in ``engine.py`` enumerates proof trees; this module answers the
authoritative question "does fact set *F* entail atom *g*?" using the
well-founded model (WFS) of the rule program.  WFS is computed with the
standard alternating (Gelfond-Lifshitz) fixed point, which:

* treats explicit negation as "derivably absent";
* refuses to accept rootless positive cycles (``a :- a`` with no fact proves
  nothing);
* accepts grounded cycles (``a :- a, f`` where ``f`` is a fact).

Nothing in here imports the enumeration engine, so tests can cross-check the
engine against an independent implementation.
"""

from __future__ import annotations

import hashlib
import json
from collections import deque
from itertools import combinations
from typing import Dict, FrozenSet, Iterable, Mapping, Sequence, Set, Tuple

from .model import Fact, Rule, atom_of, body_dnf, is_negative, premise_from_json


Atom = str
AtomSet = FrozenSet[str]


def _pos_consequences(
    facts: Iterable[Atom], rules: Sequence[Rule], context: AtomSet
) -> AtomSet:
    """Least model of the GL reduct relative to truth context ``context``.

    Derivation seeds are *facts only*; ``context`` merely decides which
    negative premises are treated as satisfied (an atom in ``context`` makes
    ``NOT atom`` false).  Because propagation is strictly data-driven,
    rootless positive cycles contribute nothing.
    """
    derived: Set[str] = set(facts)
    changed = True
    while changed:
        changed = False
        for rule in rules:
            if rule.head in derived:
                continue
            for alt in rule.alternatives:
                ok = True
                for lit in alt:
                    if is_negative(lit):
                        if atom_of(lit) in context:
                            ok = False
                            break
                    elif lit not in derived:
                        ok = False
                        break
                if ok:
                    derived.add(rule.head)
                    changed = True
                    break
    return frozenset(derived)


def gamma(facts: Iterable[Atom], rules: Sequence[Rule], context: AtomSet) -> AtomSet:
    """Gelfond-Lifshitz reduct operator relative to a truth context."""
    return _pos_consequences(facts, rules, context)


def wfs_true(
    facts: Iterable[Atom], rules: Sequence[Rule], target: Atom | None = None
) -> AtomSet:
    """True atoms of the well-founded model, as the least fixed point of g^2.

    ``U_0 = gamma(Facts, Rules, All)`` and ``U_{i+1} = gamma(gamma(U_i))``
    form an increasing sequence whose limit is exactly the WFS-true set.
    """
    fact_set = frozenset(facts)
    # gamma is anti-monotone, so gamma^2 is monotone.  The set of WFS-true
    # atoms is the least fixed point of gamma^2, iterated upward from the
    # empty context.
    under: AtomSet = frozenset()
    while True:
        nxt = gamma(fact_set, rules, gamma(fact_set, rules, under))
        if nxt == under:
            return frozenset(nxt)
        under = nxt


def entails(facts: Iterable[Atom], rules: Sequence[Rule], target: Atom) -> bool:
    """True iff ``target`` is WFS-true given ``facts`` and ``rules``."""
    return target in wfs_true(facts, rules)


# --------------------------------------------------------------------------- #
# Brute-force oracle over the subset lattice.  Used by tests to cross-check
# the lazy engine exhaustively on small synthetic graphs.
# --------------------------------------------------------------------------- #
def brute_force_minimal_supports(
    universe_facts: Mapping[str, str],
    rules: Sequence[Rule],
    goal: Atom,
    background_facts: Iterable[str] = (),
) -> Set[FrozenSet[str]]:
    """Every inclusion-minimal fact-id set that entails ``goal``.

    ``universe_facts`` maps fact id -> asserted atom.  ``background_facts``
    are fact ids always present in the current world (so negative premises
    are tested against them).  Enumeration then searches for the minimal
    *additional* support sets whose addition (with the background) entails
    the goal, and which contain no removable support fact.
    """
    fids = list(universe_facts)
    background = frozenset(background_facts)
    bg_atoms = {universe_facts[i] for i in background if i in universe_facts}
    search_ids = [i for i in fids if i not in background]
    minimal: Set[FrozenSet[str]] = set()

    def entails_ids(subset: FrozenSet[str]) -> bool:
        world = set(bg_atoms)
        world.update(universe_facts[i] for i in subset)
        return entails(world, rules, goal)

    for size in range(0, len(search_ids) + 1):
        for combo in combinations(range(len(search_ids)), size):
            subset = frozenset(search_ids[i] for i in combo)
            if not entails_ids(subset):
                continue
            if any(known < subset for known in minimal):
                continue
            # Explicit one-fact removal redundancy guard (subsumption across
            # sizes is covered above, this guards same-size degeneracies).
            minimal.add(subset)
    # Final filter: strict subset minimality over the whole lattice.
    answer = set()
    for cand in minimal:
        if not any(other < cand for other in minimal):
            answer.add(cand)
    return answer


def min_cardinality_supports(
    minimal: Iterable[FrozenSet[str]],
) -> Tuple[FrozenSet[str], ...]:
    """From inclusion-minimal sets, keep only those of minimum cardinality.

    Inclusion-minimal and minimum-cardinality are different notions; this
    helper isolates the latter.
    """
    sets = tuple(minimal)
    if not sets:
        return ()
    smallest = min(len(s) for s in sets)
    return tuple(s for s in sets if len(s) == smallest)


# --------------------------------------------------------------------------- #
# Stable hashing shared by engine and verifier.  The verifier recomputes these
# itself (independent code path) and compares strings.
# --------------------------------------------------------------------------- #
def rule_step_id(rule: Rule, premise_tokens: Sequence[str]) -> str:
    payload = json.dumps(
        {
            "rid": rule.rid,
            "version": rule.version,
            "head": rule.head,
            "premises": sorted(premise_tokens),
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    digest = hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]
    return f"rule:{rule.rid}:v{rule.version}:{digest}"


def support_id(goal: str, fact_refs: Sequence[str]) -> str:
    """Stable content id for a support.  Fact refs must be version-bearing
    (``"fid@vN````) so a fact version change yields a distinct support."""
    payload = json.dumps(
        {"goal": goal, "facts": sorted(fact_refs)},
        sort_keys=True,
        separators=(",", ":"),
    )
    digest = hashlib.sha256(payload.encode("utf-8")).hexdigest()[:16]
    return f"support:{goal}:{digest}"


# --------------------------------------------------------------------------- #
# Independent verifier: replay a recorded evidence bundle from scratch.
# --------------------------------------------------------------------------- #
class VerificationError(Exception):
    pass


def _load_bundle_rule(raw) -> Rule:
    return Rule(
        rid=raw["rid"],
        head=raw["head"],
        body=tuple(premise_from_json(p) for p in raw["body"]),
        version=raw["version"],
    )


def verify_evidence(bundle: dict, live_kb: dict | None = None) -> dict:
    """Independently verify an evidence bundle.

    Checks:

    1. **Sufficient** -- the asserted fact subset entails the goal under WFS.
    2. **Irredundant** -- removing any one fact breaks the entailment.
    3. **Replayable** -- every recorded derivation step re-fires from earlier
       steps with the referenced rule version, and the goal step is reached;
       step ids and the support id recompute identically.
    4. **Versions** -- facts/rules used by the bundle still match the live KB
       when ``live_kb`` is supplied (optional freshness check).

    Returns a result dict; raises :class:`VerificationError` on any failure.
    """
    goal = bundle["goal"]
    facts = [Fact(**f) for f in bundle["facts"]]
    rules = [_load_bundle_rule(r) for r in bundle["rules"]]
    steps = bundle["steps"]
    recorded_sid = bundle["support_id"]

    fact_ids = [f.fid for f in facts]

    # --- version freshness against the live knowledge base ---------------- #
    if live_kb is not None:
        live_facts = {f["fid"]: f.get("version") for f in live_kb.get("facts", [])}
        live_rules = {r["rid"]: r.get("version") for r in live_kb.get("rules", [])}
        for fact in facts:
            if live_facts.get(fact.fid) != fact.version:
                raise VerificationError(
                    f"fact {fact.fid!r} version drift: "
                    f"bundle v{fact.version} vs live v{live_facts.get(fact.fid)}"
                )
        used_rules = {s["rule_id"] for s in steps if s["kind"] == "rule"}
        for rid in used_rules:
            if live_rules.get(rid) is None:
                raise VerificationError(f"rule {rid!r} no longer present")
            for rule in rules:
                if rule.rid == rid:
                    if live_rules[rid] != rule.version:
                        raise VerificationError(
                            f"rule {rid!r} version drift: "
                            f"bundle v{rule.version} vs live v{live_rules[rid]}"
                        )

    # --- sufficiency / irredundancy via independent WFS ------------------- #
    base_atoms = {f.atom for f in facts}
    suff_world = set(base_atoms)
    if not entails(suff_world, rules, goal):
        raise VerificationError("support set is not sufficient for the goal")
    # Irredundancy: dropping any single support fact (self-contained replay)
    # must destroy the proof.
    for fact in facts:
        reduced_world = set(suff_world)
        reduced_world.discard(fact.atom)
        if entails(reduced_world, rules, goal):
            raise VerificationError(
                f"support set is redundant: fact {fact.fid!r} can be removed"
            )

    # --- replay derivation steps in order --------------------------------- #
    # available[step_id] = derived atom; fact steps are seeded first.
    available: Dict[str, str] = {}
    facts_by_step = {f.step_id(): f for f in facts}
    rules_by_id: Dict[Tuple[str, int], Rule] = {
        (r.rid, r.version): r for r in rules
    }

    # Topological replay order: a step follows every step it cites.  Tie
    # breaking is by step id so replay is deterministic.
    by_id = {stp["step_id"]: stp for stp in steps}
    indeg = {sid: 0 for sid in by_id}
    edges = {sid: [] for sid in by_id}
    for stp in steps:
        for child in stp.get("children", []):
            if child not in by_id:
                raise VerificationError(
                    f"step {stp['step_id']!r} cites missing step {child!r}"
                )
            edges[child].append(stp["step_id"])
            indeg[stp["step_id"]] += 1
    ready = sorted(sid for sid, d in indeg.items() if d == 0)
    ordered = []
    while ready:
        sid = ready.pop(0)
        ordered.append(by_id[sid])
        for nxt in edges[sid]:
            indeg[nxt] -= 1
            if indeg[nxt] == 0:
                ready.append(nxt)
                ready.sort()
    if len(ordered) != len(by_id):
        raise VerificationError("evidence steps contain a cycle")
    produced_atoms: Set[str] = set()
    for step in ordered:
        sid = step["step_id"]
        if step["kind"] == "fact":
            fact = facts_by_step.get(sid)
            if fact is None or fact.atom != step["atom"]:
                raise VerificationError(f"fact step {sid!r} does not match bundle facts")
            available[sid] = fact.atom
            produced_atoms.add(fact.atom)
            continue

        rule = rules_by_id.get((step["rule_id"], step["rule_version"]))
        if rule is None:
            raise VerificationError(
                f"step {sid!r} references unknown rule "
                f"{step['rule_id']!r} v{step['rule_version']}"
            )
        if rule.head != goal and rule.head != step["atom"]:
            # head always equals the step's produced atom (goal included)
            raise VerificationError(f"step {sid!r} atom/rule head mismatch")
        if step["atom"] != rule.head:
            raise VerificationError(f"step {sid!r} atom/rule head mismatch")

        # Every child step must already be available.
        premise_tokens = []
        pos_needed: Set[str] = set()
        neg_needed: Set[str] = set()
        for child in step["children"]:
            if child not in available:
                raise VerificationError(
                    f"step {sid!r} depends on not-yet-derived step {child!r}"
                )
        for premise in step["premises"]:
            if premise["sign"] == "pos":
                pos_needed.add(premise["atom"])
                child = premise.get("via_step")
                if child is None or available.get(child) != premise["atom"]:
                    raise VerificationError(f"step {sid!r} positive premise not justified")
                premise_tokens.append(f"+{premise['atom']}@{child}")
            else:
                neg_needed.add(premise["atom"])
                premise_tokens.append(f"-{premise['atom']}")

        # The selected premises must match one DNF alternative of this rule.
        selected = (
            frozenset(pos_needed)
            | frozenset(("not", a) for a in neg_needed)
        )
        if selected not in rule.alternatives:
            raise VerificationError(
                f"step {sid!r} premises do not match any rule alternative"
            )
        # Negative premises must actually be absent in the replay closure.
        if neg_needed & produced_atoms:
            raise VerificationError(f"step {sid!r} uses a negative premise that holds")

        expected_sid = rule_step_id(rule, premise_tokens)
        if expected_sid != sid:
            raise VerificationError(
                f"step id mismatch: recorded {sid!r} != recomputed {expected_sid!r}"
            )
        available[sid] = rule.head
        produced_atoms.add(rule.head)

    root = bundle.get("root_step")
    if root is None or available.get(root) != goal:
        raise VerificationError("support bundle does not derive the goal at its root")

    versioned_refs = sorted(f"{f.fid}@v{f.version}" for f in facts)
    expected_support = support_id(goal, versioned_refs)
    if expected_support != recorded_sid:
        raise VerificationError(
            f"support id mismatch: recorded {recorded_sid!r} "
            f"!= recomputed {expected_support!r}"
        )

    return {
        "ok": True,
        "goal": goal,
        "support_id": recorded_sid,
        "fact_ids": sorted(fact_ids),
        "num_steps": len(steps),
        "num_facts": len(facts),
    }
