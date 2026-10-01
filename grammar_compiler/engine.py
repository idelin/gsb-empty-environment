"""Incremental nullable/FIRST/FOLLOW engine with provenance tracking.

Every derived set element ("fact") keeps the full set of proofs that support
it.  A proof records the source production, the conclusion fact and the
antecedent facts it was derived from.  When a production is removed or its
condition is disabled, its proofs are retracted; a fact is withdrawn exactly
when its last supporting proof disappears, and the retraction cascades to all
proofs that used the fact as an antecedent.  Adding facts re-derives only the
productions that mention the affected symbol, so updates are incremental.
"""
from __future__ import annotations

from collections import defaultdict, deque
from dataclasses import dataclass

from .symbols import END, Production, Sym

NULLABLE = "nullable"
FIRST = "first"
FOLLOW = "follow"


@dataclass(frozen=True)
class Proof:
    """One piece of evidence: conclusion follows from antecedents via source."""

    source: tuple          # ("prod", pid, template...) or ("seed", tag)
    conclusion: tuple      # fact, e.g. ("first", Sym, Sym)
    antecedents: tuple     # facts this proof depends on


class IncrementalAnalysis:
    def __init__(self) -> None:
        self.productions: dict[int, Production] = {}
        self.active: set[int] = set()
        self.enabled_conditions: set[str] = set()
        self.facts: dict[tuple, set[Proof]] = {}
        self.dependents: dict[tuple, set[Proof]] = defaultdict(set)
        self.prod_proofs: dict[int, set[Proof]] = defaultdict(set)
        self.by_symbol: dict[Sym, set[int]] = defaultdict(set)
        # materialized views (kept in sync with self.facts)
        self.nullable: set[Sym] = set()
        self.first: dict[Sym, set[Sym]] = defaultdict(set)
        self.follow: dict[Sym, set[Sym]] = defaultdict(set)
        self.start: Sym | None = None
        self._seed_proofs: set[Proof] = set()
        self._worklist: deque = deque()
        self.revision = 0

    # ------------------------------------------------------------------ views
    def _view_add(self, fact: tuple) -> None:
        kind, sym = fact[0], fact[1]
        if kind == NULLABLE:
            self.nullable.add(sym)
        elif kind == FIRST:
            self.first[sym].add(fact[2])
        else:
            self.follow[sym].add(fact[2])

    def _view_remove(self, fact: tuple) -> None:
        kind, sym = fact[0], fact[1]
        if kind == NULLABLE:
            self.nullable.discard(sym)
        else:
            view = self.first if kind == FIRST else self.follow
            entries = view.get(sym)
            if entries is not None:
                entries.discard(fact[2])
                if not entries:
                    del view[sym]

    # ----------------------------------------------------------------- proofs
    def _insert_proof(self, proof: Proof) -> None:
        supports = self.facts.get(proof.conclusion)
        if supports is not None and proof in supports:
            return
        new_fact = supports is None
        if new_fact:
            supports = self.facts[proof.conclusion] = set()
        supports.add(proof)
        for antecedent in proof.antecedents:
            self.dependents[antecedent].add(proof)
        if proof.source[0] == "prod":
            self.prod_proofs[proof.source[1]].add(proof)
        if new_fact:
            self._view_add(proof.conclusion)
            self._worklist.append(proof.conclusion)

    def _drop_proof(self, proof: Proof) -> None:
        """Remove a single proof object from every index (no cascade)."""
        supports = self.facts.get(proof.conclusion)
        if supports is not None:
            supports.discard(proof)
            if not supports:
                del self.facts[proof.conclusion]
                self._view_remove(proof.conclusion)
        for antecedent in proof.antecedents:
            deps = self.dependents.get(antecedent)
            if deps is not None:
                deps.discard(proof)
                if not deps:
                    del self.dependents[antecedent]
        if proof.source[0] == "prod":
            own = self.prod_proofs.get(proof.source[1])
            if own is not None:
                own.discard(proof)
                if not own:
                    del self.prod_proofs[proof.source[1]]

    def _retract_proofs(self, proofs) -> None:
        """DRed-style retraction: over-delete the dependent cone, re-derive.

        Recursive rules allow cyclic proof chains in which facts keep each
        other alive after their real support is gone.  Exact reference
        counting cannot detect this, so retraction deletes every fact that
        has *any* derivation depending on a retracted proof, then re-derives
        the still-valid subset from the surviving facts.
        """
        cone = set()
        queue = deque()
        for proof in proofs:
            if proof.conclusion not in cone:
                cone.add(proof.conclusion)
                queue.append(proof.conclusion)
        while queue:
            fact = queue.popleft()
            for dependent in self.dependents.get(fact, ()):
                if dependent.conclusion not in cone:
                    cone.add(dependent.conclusion)
                    queue.append(dependent.conclusion)
        for fact in cone:
            for proof in list(self.facts.get(fact, ())):
                self._drop_proof(proof)
            self.facts.pop(fact, None)
            self.dependents.pop(fact, None)
        # seed proofs encode desired state (start symbol); re-plant them
        for seed in self._seed_proofs:
            self._insert_proof(seed)
        symbols = {fact[1] for fact in cone}
        for sym in symbols:
            for pid in list(self.by_symbol.get(sym, ())):
                self._derive(pid)
        self._process()

    # -------------------------------------------------------------- derivation
    def _derive(self, pid: int) -> None:
        """(Re)evaluate every template of one active production.

        Proof identity is structural, so re-derivation is idempotent and only
        genuinely new proofs/facts are added.
        """
        prod = self.productions.get(pid)
        if prod is None or pid not in self.active:
            return
        lhs, rhs = prod.lhs, prod.rhs
        src = ("prod", pid)
        facts = self.facts

        # nullable: lhs nullable iff rhs has no terminals and every
        # nonterminal in rhs is nullable (empty rhs qualifies)
        if all(s.is_nonterminal for s in rhs):
            ante = tuple((NULLABLE, s) for s in rhs)
            if all(a in facts for a in ante):
                self._insert_proof(
                    Proof(src + ("nullable",), (NULLABLE, lhs), ante))

        # first: scan rhs from the left across nullable nonterminals
        prefix: list[tuple] = []
        for i, sym in enumerate(rhs):
            if sym.is_terminal:
                self._insert_proof(
                    Proof(src + ("first", i), (FIRST, lhs, sym), tuple(prefix)))
                break
            for tok in list(self.first.get(sym, ())):
                self._insert_proof(Proof(
                    src + ("first", i),
                    (FIRST, lhs, tok),
                    tuple(prefix) + ((FIRST, sym, tok),)))
            nullable_fact = (NULLABLE, sym)
            if nullable_fact in facts:
                prefix.append(nullable_fact)
            else:
                break

        # follow: scan rhs from the right
        n = len(rhs)
        suffix: list[tuple] = []       # nullable facts of rhs[i+1..n-1]
        suffix_nullable = True
        for i in range(n - 1, -1, -1):
            sym = rhs[i]
            if sym.is_nonterminal:
                if suffix_nullable:
                    for tok in list(self.follow.get(lhs, ())):
                        self._insert_proof(Proof(
                            src + ("follow-end", i),
                            (FOLLOW, sym, tok),
                            tuple(suffix) + ((FOLLOW, lhs, tok),)))
                mid: list[tuple] = []
                for j in range(i + 1, n):
                    nxt = rhs[j]
                    if nxt.is_terminal:
                        self._insert_proof(Proof(
                            src + ("follow-mid", i, j),
                            (FOLLOW, sym, nxt), tuple(mid)))
                        break
                    for tok in list(self.first.get(nxt, ())):
                        self._insert_proof(Proof(
                            src + ("follow-mid", i, j),
                            (FOLLOW, sym, tok),
                            tuple(mid) + ((FIRST, nxt, tok),)))
                    nullable_fact = (NULLABLE, nxt)
                    if nullable_fact in facts:
                        mid.append(nullable_fact)
                    else:
                        break
                nullable_fact = (NULLABLE, sym)
                if nullable_fact in facts:
                    suffix.append(nullable_fact)
                else:
                    suffix_nullable = False
                    suffix = []
            else:
                suffix_nullable = False
                suffix = []

    def _process(self) -> None:
        while self._worklist:
            fact = self._worklist.popleft()
            sym = fact[1]
            for pid in list(self.by_symbol.get(sym, ())):
                self._derive(pid)

    # ------------------------------------------------------------- public API
    def add_production(self, prod: Production) -> None:
        pid = prod.pid
        if pid in self.productions:
            raise ValueError(f"duplicate production id {pid}")
        self.productions[pid] = prod
        for sym in set(prod.rhs) | {prod.lhs}:
            self.by_symbol[sym].add(pid)
        if prod.condition is None or prod.condition in self.enabled_conditions:
            self.active.add(pid)
            self._derive(pid)
            self._process()
        self.revision += 1

    def remove_production(self, pid: int) -> None:
        prod = self.productions.pop(pid, None)
        if prod is None:
            raise KeyError(f"unknown production id {pid}")
        self._deactivate(pid)
        for sym in set(prod.rhs) | {prod.lhs}:
            owners = self.by_symbol.get(sym)
            if owners is not None:
                owners.discard(pid)
                if not owners:
                    del self.by_symbol[sym]
        self.revision += 1

    def _activate(self, pid: int) -> None:
        if pid in self.active or pid not in self.productions:
            return
        self.active.add(pid)
        self._derive(pid)
        self._process()

    def _deactivate(self, pid: int) -> None:
        if pid not in self.active:
            return
        self.active.discard(pid)
        self._retract_proofs(list(self.prod_proofs.get(pid, ())))

    def enable_condition(self, condition: str) -> None:
        if condition in self.enabled_conditions:
            return
        self.enabled_conditions.add(condition)
        for pid, prod in list(self.productions.items()):
            if prod.condition == condition:
                self._activate(pid)
        self.revision += 1

    def disable_condition(self, condition: str) -> None:
        if condition not in self.enabled_conditions:
            return
        self.enabled_conditions.discard(condition)
        for pid, prod in list(self.productions.items()):
            if prod.condition == condition:
                self._deactivate(pid)
        self.revision += 1

    def set_start(self, sym: Sym | None) -> None:
        if self.start == sym:
            return
        old_seeds = list(self._seed_proofs)
        self._seed_proofs = set()
        self._retract_proofs(old_seeds)
        self.start = sym
        if sym is not None:
            seed = Proof(("seed", "follow-end"), (FOLLOW, sym, END), ())
            self._seed_proofs.add(seed)
            self._insert_proof(seed)
            self._process()
        self.revision += 1

    # ---------------------------------------------------------------- queries
    def proofs_of(self, fact: tuple) -> frozenset:
        return frozenset(self.facts.get(fact, ()))

    def explain(self, fact: tuple) -> list[str]:
        lines = []
        for proof in sorted(self.facts.get(fact, ()), key=repr):
            ants = ", ".join(map(str, proof.antecedents)) or "nothing"
            lines.append(f"{proof.conclusion}  because  [{ants}]  via {proof.source}")
        return lines

    def check_invariants(self) -> None:
        for fact, proofs in self.facts.items():
            assert proofs, f"fact without evidence: {fact}"
            for proof in proofs:
                assert proof.conclusion == fact
                for antecedent in proof.antecedents:
                    assert antecedent in self.facts, (proof, antecedent)
                    assert proof in self.dependents[antecedent]
        for fact, deps in self.dependents.items():
            assert deps, f"empty dependent bucket: {fact}"
            assert fact in self.facts
