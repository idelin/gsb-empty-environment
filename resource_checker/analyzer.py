"""Forward abstract interpretation + bounded concrete replay classification.

The analysis explores the finite-state model with an explicit powerset
abstract domain (one set of abstract states per program point, joined by
union).  It checks the resource-ownership invariant on **all** exits --
normal, exceptional and cancellation -- and searches exception cleanup
(finally) and cancellation paths like any other path.

Ownership rules enforced:

- after ``transfer(src, dst)`` the source owner is invalidated; releasing
  (or using / re-transferring) through it is a ``*_after_move`` error;
- releasing an already released resource is a separate ``double_release``
  error;
- releasing / transferring a resource with active borrows is an error;
- any resource still owned (or maybe-owned) at any exit is a leak
  candidate.

Every candidate is then classified by bounded concrete replay:

- ``definite`` : confirmed by concrete replay (replayable counterexample);
                 for leaks this means *every* concrete run of the witness
                 path leaks (must-leak);
- ``possible`` : some but not all concrete runs confirm it, or the replay
                 bound was exceeded before a verdict could be reached;
- ``filtered`` : no concrete run confirms it -- an abstract false
                 positive, removed from the report.

If the abstract search itself hits its step bound, the overall verdict
stays ``pending`` (the model has not been fully searched).
"""
from __future__ import annotations

from dataclasses import dataclass, field

from . import concrete as C
from . import model as M
from .domain import MAYBE_RELEASED, MOVED, NULL, OWNED, RELEASED, AbsState, ResInfo, StateSpace

LEAK = "leak"

ERROR_KINDS = frozenset(
    {
        C.DOUBLE_RELEASE,
        C.RELEASE_AFTER_MOVE,
        C.RELEASE_OF_NULL,
        C.RELEASE_WHILE_BORROWED,
        C.TRANSFER_AFTER_MOVE,
        C.TRANSFER_OF_NULL,
        C.TRANSFER_AFTER_RELEASE,
        C.TRANSFER_WHILE_BORROWED,
        C.USE_AFTER_MOVE,
        C.USE_OF_NULL,
        C.USE_AFTER_RELEASE,
        C.BORROW_AFTER_MOVE,
        C.BORROW_OF_NULL,
        C.BORROW_AFTER_RELEASE,
    }
)

# verdicts
SAFE = "safe"
VIOLATED = "violated"
WARNING = "warning"
PENDING = "pending"


@dataclass
class Finding:
    kind: str                 # LEAK or one of ERROR_KINDS
    status: str               # "definite" | "possible" | "filtered"
    resource: str | None      # abstract resource id ("r@<node>")
    node: str                 # error node, or exit node for leaks
    path: tuple[tuple[str, str], ...]
    exit_kind: str | None = None
    evidence: str = ""

    def path_str(self) -> str:
        if not self.path:
            return ""
        out = [self.path[0][0]]
        for node, kind in self.path[1:]:
            out.append(f"-[{kind}]-> {node}")
        return " ".join(out)


@dataclass
class Report:
    status: str               # SAFE | VIOLATED | WARNING | PENDING
    findings: list[Finding]   # definite + possible
    filtered: list[Finding]   # abstract false positives removed by replay
    states_explored: int
    complete: bool            # False => search bound hit, verdict is pending

    def definite(self) -> list[Finding]:
        return [f for f in self.findings if f.status == "definite"]

    def possible(self) -> list[Finding]:
        return [f for f in self.findings if f.status == "possible"]


class Analyzer:
    def __init__(self, program: M.Program, max_steps: int = 5000, replay_max_runs: int = 256):
        self.program = program
        self.max_steps = max_steps
        self.replay_max_runs = replay_max_runs
        self.space = StateSpace()
        self.candidates: list[Finding] = []
        self.complete = True

    # ------------------------------------------------------------------ run
    def run(self) -> Report:
        entry_path = ((self.program.entry, "entry"),)
        work: list[tuple[str, AbsState, tuple]] = [
            (self.program.entry, AbsState(), entry_path)
        ]
        steps = 0
        while work:
            node_name, state, path = work.pop()
            if not self.space.join(node_name, state):
                continue
            if steps >= self.max_steps:
                self.complete = False  # search not finished: verdict stays pending
                break
            steps += 1
            node = self.program.nodes[node_name]
            post, halt = self._step(node, state, path)
            if halt:
                continue
            if not node.edges:
                self._check_exit(node, post, path)
                continue
            for edge in node.edges:
                work.append((edge.target, post, path + ((edge.target, edge.kind),)))

        findings, filtered = self._classify_all()
        if not self.complete:
            status = PENDING
        elif any(f.status == "definite" for f in findings):
            status = VIOLATED
        elif findings:
            status = WARNING
        else:
            status = SAFE
        findings.sort(key=lambda f: (f.status != "definite", f.kind, f.node))
        return Report(
            status=status,
            findings=findings,
            filtered=filtered,
            states_explored=self.space.total(),
            complete=self.complete,
        )

    # ------------------------------------------------- abstract transfer --
    def _candidate(
        self,
        kind: str,
        resource: str | None,
        node: str,
        path: tuple,
        exit_kind: str | None = None,
    ) -> None:
        self.candidates.append(
            Finding(kind=kind, status="candidate", resource=resource, node=node,
                    path=path, exit_kind=exit_kind)
        )

    @staticmethod
    def _rid_tokens(tokens: frozenset[str]) -> list[str]:
        return sorted(t for t in tokens if t not in (NULL, MOVED))

    def _step(self, node: M.Node, state: AbsState, path: tuple) -> tuple[AbsState, bool]:
        """Abstract transfer function.  Returns (post_state, halt)."""
        op = node.op
        kind = op.kind
        if kind == M.NOP:
            return state, False
        if kind == M.ACQUIRE:
            (var,) = op.args
            rid = f"r@{node.name}"
            new = state
            for tok in new.lookup(var):
                if tok not in (NULL, MOVED) and tok in new.res:
                    info = new.res[tok]
                    new = new.with_res(tok, ResInfo(info.state, None, info.borrows))
            old = new.res.get(rid)
            if old is not None and old.state != RELEASED:
                # re-acquire overwrites a live abstract resource: the previous
                # incarnation is abandoned (loop leak candidate)
                self._candidate(LEAK, rid, node.name, path,
                                exit_kind="overwrite")
            new = new.with_env(var, {rid})
            new = new.with_res(rid, ResInfo(OWNED, var, frozenset()))
            return new, False
        if kind == M.ALIAS:
            dst, src = op.args
            return state.with_env(dst, state.lookup(src)), False
        if kind == M.MAYBE_ALIAS:
            dst, src = op.args
            merged = state.lookup(dst) | state.lookup(src)
            return state.with_env(dst, merged), False
        if kind == M.RELEASE:
            return self._release(node, state, path)
        if kind == M.TRANSFER:
            return self._transfer(node, state, path)
        if kind == M.BORROW:
            return self._borrow(node, state, path)
        if kind == M.END_BORROW:
            (var,) = op.args
            new = state
            for rid in self._rid_tokens(new.lookup(var)):
                info = new.res.get(rid)
                if info is not None and info.borrows:
                    new = new.with_res(rid, ResInfo(info.state, info.owner, frozenset()))
            return new, False
        if kind == M.USE:
            return self._use(node, state, path)
        raise ValueError(f"unknown op kind {kind!r}")

    def _release(self, node: M.Node, state: AbsState, path: tuple) -> tuple[AbsState, bool]:
        (var,) = node.op.args
        tokens = state.lookup(var)
        precise = len(tokens) == 1
        rids = self._rid_tokens(tokens)
        if tokens == frozenset({MOVED}):
            self._candidate(C.RELEASE_AFTER_MOVE, None, node.name, path)
            return state, True
        if not rids:
            self._candidate(C.RELEASE_OF_NULL, None, node.name, path)
            return state, True
        new = state
        if MOVED in tokens:
            self._candidate(C.RELEASE_AFTER_MOVE, None, node.name, path)
        if NULL in tokens:
            self._candidate(C.RELEASE_OF_NULL, None, node.name, path)
        for rid in rids:
            info = new.res.get(rid)
            if info is None:
                continue
            if info.state == RELEASED:
                self._candidate(C.DOUBLE_RELEASE, rid, node.name, path)
                if precise:
                    return new, True
                continue
            if info.borrows:
                self._candidate(C.RELEASE_WHILE_BORROWED, rid, node.name, path)
                if precise:
                    return new, True
            if precise:
                new = new.with_res(rid, ResInfo(RELEASED, None, info.borrows))
            elif info.state == OWNED:
                new = new.with_res(rid, ResInfo(MAYBE_RELEASED, info.owner, info.borrows))
            # weak release of a maybe-released resource keeps it maybe-released
        return new, False

    def _transfer(self, node: M.Node, state: AbsState, path: tuple) -> tuple[AbsState, bool]:
        src, dst = node.op.args
        tokens = state.lookup(src)
        precise = len(tokens) == 1
        rids = self._rid_tokens(tokens)
        if tokens == frozenset({MOVED}):
            self._candidate(C.TRANSFER_AFTER_MOVE, None, node.name, path)
            return state, True
        if not rids:
            self._candidate(C.TRANSFER_OF_NULL, None, node.name, path)
            return state, True
        new = state
        if MOVED in tokens:
            self._candidate(C.TRANSFER_AFTER_MOVE, None, node.name, path)
        if NULL in tokens:
            self._candidate(C.TRANSFER_OF_NULL, None, node.name, path)
        live: list[str] = []
        for rid in rids:
            info = new.res.get(rid)
            if info is None:
                continue
            if info.state == RELEASED:
                self._candidate(C.TRANSFER_AFTER_RELEASE, rid, node.name, path)
                if precise:
                    return new, True
                continue
            if info.borrows:
                self._candidate(C.TRANSFER_WHILE_BORROWED, rid, node.name, path)
                if precise:
                    return new, True
            live.append(rid)
        if live:
            for tok in new.lookup(dst):
                if tok not in (NULL, MOVED) and tok in new.res:
                    info = new.res[tok]
                    new = new.with_res(tok, ResInfo(info.state, None, info.borrows))
            new = new.with_env(dst, set(live))
            for rid in live:
                info = new.res[rid]
                new = new.with_res(rid, ResInfo(info.state, dst, info.borrows))
            new = new.with_env(src, {MOVED})
        return new, False

    def _borrow(self, node: M.Node, state: AbsState, path: tuple) -> tuple[AbsState, bool]:
        (var,) = node.op.args
        tokens = state.lookup(var)
        precise = len(tokens) == 1
        rids = self._rid_tokens(tokens)
        if tokens == frozenset({MOVED}):
            self._candidate(C.BORROW_AFTER_MOVE, None, node.name, path)
            return state, True
        if not rids:
            self._candidate(C.BORROW_OF_NULL, None, node.name, path)
            return state, True
        new = state
        for rid in rids:
            info = new.res.get(rid)
            if info is None:
                continue
            if info.state == RELEASED:
                self._candidate(C.BORROW_AFTER_RELEASE, rid, node.name, path)
                if precise:
                    return new, True
                continue
            tag = f"borrow@{node.name}"
            new = new.with_res(rid, ResInfo(info.state, info.owner, info.borrows | {tag}))
        return new, False

    def _use(self, node: M.Node, state: AbsState, path: tuple) -> tuple[AbsState, bool]:
        (var,) = node.op.args
        tokens = state.lookup(var)
        precise = len(tokens) == 1
        rids = self._rid_tokens(tokens)
        if tokens == frozenset({MOVED}):
            self._candidate(C.USE_AFTER_MOVE, None, node.name, path)
            return state, True
        if not rids:
            self._candidate(C.USE_OF_NULL, None, node.name, path)
            return state, True
        for rid in rids:
            info = state.res.get(rid)
            if info is not None and info.state == RELEASED:
                self._candidate(C.USE_AFTER_RELEASE, rid, node.name, path)
                if precise:
                    return state, True
        return state, False

    # -------------------------------------------------------- exit check --
    def _check_exit(self, node: M.Node, state: AbsState, path: tuple) -> None:
        exit_kind = self.program.exit_kind(node.name) or M.EXIT_NORMAL
        for rid, info in sorted(state.res.items()):
            if info.state in (OWNED, MAYBE_RELEASED):
                self._candidate(LEAK, rid, node.name, path, exit_kind=exit_kind)

    # ---------------------------------------------- replay classification --
    def _classify_all(self) -> tuple[list[Finding], list[Finding]]:
        unique: dict[tuple, Finding] = {}
        for cand in self.candidates:
            key = (cand.kind, cand.resource, cand.node, cand.exit_kind)
            unique.setdefault(key, cand)
        findings: list[Finding] = []
        filtered: list[Finding] = []
        for cand in unique.values():
            result = self._classify(cand)
            (filtered if result.status == "filtered" else findings).append(result)
        return findings, filtered

    def _classify(self, cand: Finding) -> Finding:
        result = C.replay(self.program, cand.path, self.replay_max_runs)
        if cand.kind == LEAK:
            return self._classify_leak(cand, result)
        return self._classify_error(cand, result)

    def _classify_leak(self, cand: Finding, result: C.ReplayResult) -> Finding:
        acquire_node = (cand.resource or "")[2:]  # "r@<node>" -> "<node>"
        if not result.runs:
            cand.status = "filtered"
            cand.evidence = "replay: path is concretely infeasible (false positive)"
            return cand
        leaking = [r for r in result.runs if C.leaked_resources(r, acquire_node)]
        witness = leaking[0] if leaking else None
        if leaking and len(leaking) == len(result.runs) and result.complete:
            cand.status = "definite"
            cand.evidence = (
                f"replay: all {len(result.runs)} concrete run(s) leak; "
                f"witness keeps {self._fmt_leaked(witness, acquire_node)}"
            )
        elif leaking:
            cand.status = "possible"
            note = "" if result.complete else " (replay bound hit)"
            cand.evidence = (
                f"replay: {len(leaking)}/{len(result.runs)} concrete run(s) leak{note}; "
                f"witness keeps {self._fmt_leaked(witness, acquire_node)}"
            )
        elif not result.complete:
            cand.status = "possible"
            cand.evidence = "replay: inconclusive, nondeterminism bound exceeded"
        else:
            cand.status = "filtered"
            cand.evidence = (
                f"replay: 0/{len(result.runs)} concrete run(s) leak (false positive)"
            )
        return cand

    @staticmethod
    def _fmt_leaked(run: C.CRun, acquire_node: str) -> str:
        parts = []
        for rid in sorted(C.leaked_resources(run, acquire_node)):
            owner = run.res[rid].owner
            parts.append(f"{rid} owned by {owner}" if owner else f"{rid} owned (owner lost)")
        return "; ".join(parts)

    def _classify_error(self, cand: Finding, result: C.ReplayResult) -> Finding:
        confirming = [
            r
            for r in result.runs
            if any(e.node == cand.node and e.kind == cand.kind for e in r.events)
        ]
        if confirming:
            run = confirming[0]
            event = next(
                e for e in run.events if e.node == cand.node and e.kind == cand.kind
            )
            cand.status = "definite"
            res = f" on {event.resource}" if event.resource else ""
            cand.evidence = (
                f"replay: concrete run confirms {event.kind}{res} at {event.node}"
            )
        elif not result.complete:
            cand.status = "possible"
            cand.evidence = "replay: inconclusive, nondeterminism bound exceeded"
        else:
            cand.status = "filtered"
            cand.evidence = "replay: no concrete run confirms it (false positive)"
        return cand
