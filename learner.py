"""L*-style observation-table learner.

The learner may only ask membership queries (through the ``membership``
callable) and bounded equivalence queries (through an oracle object with
``find_counterexample(hypothesis)``).  It never sees the hidden machine.

Design notes:

* The observation table is kept closed and consistent; hypotheses are
  built from distinct rows of S.
* Every table cell is resolved by a real membership query (served from
  the cache when already known).  Undetermined cells are never silently
  treated as rejection; reading one raises an error.
* Counterexamples are processed by enqueueing their distinguishing
  suffixes into E (Rivest-Schapire style).  The pending queue is part of
  the checkpoint, so an interrupted analysis resumes exactly where it
  stopped.
* A total membership-query budget is enforced.  On exhaustion the
  learner saves S, E, the cache, the pending-suffix queue and all
  counters; resuming continues with identical behaviour.
* Checkpoints bind the alphabet, L and the oracle version.  A mismatch
  rejects the old evidence.
"""
from __future__ import annotations

import json
from dataclasses import dataclass

from dfa import DFA

CHECKPOINT_FORMAT = "obs-table-checkpoint-v1"

STATUS_LEARNING = "learning"
STATUS_EQUIVALENT_IN_DOMAIN = "equivalent_in_domain"
STATUS_BUDGET_UNDECIDED = "budget_undecided"

_NOTE_EQ = ("hypothesis agrees with the oracle on every word of length "
            "<= L; no claim is made about longer words")
_NOTE_BUDGET = "membership-query budget exhausted; learning undecided"


class BudgetExhausted(Exception):
    """Internal control-flow signal: the query budget ran out."""


class CheckpointMismatch(Exception):
    """Checkpoint binding (alphabet, L, oracle version) does not match."""


@dataclass
class RoundStat:
    round_index: int
    num_states: int
    new_membership_queries: int
    cache_hits: int
    counterexample: "str | None"


@dataclass
class LearnResult:
    status: str
    hypothesis: "DFA | None"
    rounds: list
    counterexamples: list
    note: str


class ObservationTableLearner:
    def __init__(self, alphabet, max_len, membership, oracle_version, budget=None):
        self.alphabet = tuple(alphabet)
        if not self.alphabet:
            raise ValueError("alphabet must be non-empty")
        self.max_len = max_len
        self.membership = membership
        self.oracle_version = oracle_version
        # budget caps the total number of *new* membership queries
        # (self.used); None means unlimited.
        self.budget = budget
        self.S = [""]
        self.E = [""]
        self.cache = {}
        self.pending_suffixes = []
        self.used = 0
        self.new_queries = 0
        self.cache_hits = 0
        self.rounds = []
        self.counterexamples = []
        self.status = STATUS_LEARNING
        self._round_new = 0
        self._round_hits = 0

    # ------------------------------------------------------------------
    # membership queries with cache and budget
    # ------------------------------------------------------------------
    def _mq(self, word):
        if word in self.cache:
            self.cache_hits += 1
            self._round_hits += 1
            return self.cache[word]
        if self.budget is not None and self.used >= self.budget:
            raise BudgetExhausted
        self.used += 1
        self.new_queries += 1
        self._round_new += 1
        self.cache[word] = bool(self.membership(word))
        return self.cache[word]

    # ------------------------------------------------------------------
    # observation table
    # ------------------------------------------------------------------
    def _row_prefixes(self):
        prefixes = list(self.S)
        for s in self.S:
            for a in self.alphabet:
                prefixes.append(s + a)
        return prefixes

    def _fill(self):
        """Resolve every table cell with (cached) membership queries."""
        for s in self._row_prefixes():
            for e in self.E:
                self._mq(s + e)

    def _cell(self, word):
        try:
            return self.cache[word]
        except KeyError:
            raise RuntimeError(
                f"undetermined table cell for {word!r}; "
                "cells must be resolved by membership queries, never assumed")

    def _row(self, s):
        return tuple(self._cell(s + e) for e in self.E)

    def _find_unclosed(self):
        rows = {self._row(s) for s in self.S}
        for s in self.S:
            for a in self.alphabet:
                if self._row(s + a) not in rows:
                    return s + a
        return None

    def _find_inconsistency(self):
        by_row = {}
        for s in self.S:
            by_row.setdefault(self._row(s), []).append(s)
        for group in by_row.values():
            for i in range(len(group)):
                for j in range(i + 1, len(group)):
                    s1, s2 = group[i], group[j]
                    for a in self.alphabet:
                        r1 = self._row(s1 + a)
                        r2 = self._row(s2 + a)
                        if r1 != r2:
                            for k, e in enumerate(self.E):
                                if r1[k] != r2[k]:
                                    return a + e
        return None

    def _close_and_consistent(self):
        while True:
            self._fill()
            unclosed = self._find_unclosed()
            if unclosed is not None:
                self.S.append(unclosed)
                continue
            new_suffix = self._find_inconsistency()
            if new_suffix is not None:
                if new_suffix not in self.E:
                    self.E.append(new_suffix)
                continue
            return

    # ------------------------------------------------------------------
    # hypothesis construction
    # ------------------------------------------------------------------
    def _build_hypothesis(self):
        row_index = {}
        for s in self.S:
            r = self._row(s)
            if r not in row_index:
                row_index[r] = len(row_index)
        start = row_index[self._row("")]
        transitions = {}
        accepting = set()
        for s in self.S:
            r = self._row(s)
            i = row_index[r]
            if r[0]:  # E[0] is always the empty suffix
                accepting.add(i)
            for a in self.alphabet:
                transitions[(i, a)] = row_index[self._row(s + a)]
        return DFA(len(row_index), self.alphabet, start, transitions, accepting)

    def current_hypothesis(self):
        self._flush_pending()
        self._close_and_consistent()
        return self._build_hypothesis()

    # ------------------------------------------------------------------
    # counterexample handling
    # ------------------------------------------------------------------
    def _enqueue_counterexample(self, ce):
        added = False
        for i in range(len(ce) + 1):
            suffix = ce[i:]
            if suffix not in self.E and suffix not in self.pending_suffixes:
                self.pending_suffixes.append(suffix)
                added = True
        if not added:
            raise RuntimeError(
                f"counterexample {ce!r} yields no new distinguishing suffix; "
                "it is consistent with the current table")

    def _flush_pending(self):
        while self.pending_suffixes:
            e = self.pending_suffixes.pop(0)
            if e not in self.E:
                self.E.append(e)

    def add_external_counterexample(self, ce):
        """Verify an externally supplied counterexample before using it.

        A membership query (served from the cache when possible) checks
        the claim against the hidden machine.  A false counterexample is
        rejected and the table is left untouched.  Returns True iff the
        counterexample was accepted.
        """
        for a in ce:
            if a not in self.alphabet:
                raise ValueError(f"symbol {a!r} not in alphabet")
        if len(ce) > self.max_len:
            raise ValueError("counterexample lies outside the query domain")
        hyp = self.current_hypothesis()
        actual = self._mq(ce)
        if hyp.accepts(ce) == actual:
            return False
        self.counterexamples.append(ce)
        self._enqueue_counterexample(ce)
        return True

    # ------------------------------------------------------------------
    # main loop
    # ------------------------------------------------------------------
    def _record_round(self, hyp, ce):
        self.rounds.append(RoundStat(
            round_index=len(self.rounds),
            num_states=hyp.num_states,
            new_membership_queries=self._round_new,
            cache_hits=self._round_hits,
            counterexample=ce,
        ))
        self._round_new = 0
        self._round_hits = 0

    def learn(self, eq_oracle):
        """Run until in-domain equivalence or budget exhaustion."""
        try:
            while True:
                self._flush_pending()
                self._close_and_consistent()
                hyp = self._build_hypothesis()
                ce = eq_oracle.find_counterexample(hyp)
                self._record_round(hyp, ce)
                if ce is None:
                    self.status = STATUS_EQUIVALENT_IN_DOMAIN
                    return LearnResult(self.status, hyp, list(self.rounds),
                                       list(self.counterexamples), _NOTE_EQ)
                self.counterexamples.append(ce)
                self._enqueue_counterexample(ce)
        except BudgetExhausted:
            self.status = STATUS_BUDGET_UNDECIDED
            return LearnResult(self.status, None, list(self.rounds),
                               list(self.counterexamples), _NOTE_BUDGET)

    # ------------------------------------------------------------------
    # checkpointing
    # ------------------------------------------------------------------
    def _binding(self):
        return {
            "alphabet": list(self.alphabet),
            "max_len": self.max_len,
            "oracle_version": self.oracle_version,
        }

    def save_checkpoint(self, path):
        data = {
            "format": CHECKPOINT_FORMAT,
            "binding": self._binding(),
            "S": self.S,
            "E": self.E,
            "cache": self.cache,
            "pending_suffixes": self.pending_suffixes,
            "used": self.used,
            "new_queries": self.new_queries,
            "cache_hits": self.cache_hits,
            "round_new": self._round_new,
            "round_hits": self._round_hits,
            "counterexamples": self.counterexamples,
            "rounds": [vars(r) for r in self.rounds],
            "status": self.status,
        }
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2)

    @classmethod
    def resume_from_checkpoint(cls, path, membership, oracle_version,
                               budget=None, alphabet=None, max_len=None):
        """Restore a learner from a checkpoint.

        ``budget`` is the number of *additional* membership queries the
        resumed session may ask.  The checkpoint is rejected when the
        bound alphabet, L or oracle version does not match.
        """
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
        if data.get("format") != CHECKPOINT_FORMAT:
            raise CheckpointMismatch("unknown checkpoint format")
        binding = data["binding"]
        if alphabet is not None and tuple(alphabet) != tuple(binding["alphabet"]):
            raise CheckpointMismatch("alphabet does not match checkpoint")
        if max_len is not None and max_len != binding["max_len"]:
            raise CheckpointMismatch("max_len does not match checkpoint")
        if oracle_version != binding["oracle_version"]:
            raise CheckpointMismatch(
                "oracle version mismatch; refusing to reuse old evidence")
        learner = cls(tuple(binding["alphabet"]), binding["max_len"],
                      membership, oracle_version, budget=None)
        learner.S = list(data["S"])
        learner.E = list(data["E"])
        learner.cache = {w: bool(v) for w, v in data["cache"].items()}
        learner.pending_suffixes = list(data["pending_suffixes"])
        learner.used = data["used"]
        learner.new_queries = data["new_queries"]
        learner.cache_hits = data["cache_hits"]
        learner._round_new = data["round_new"]
        learner._round_hits = data["round_hits"]
        learner.counterexamples = list(data["counterexamples"])
        learner.rounds = [RoundStat(**r) for r in data["rounds"]]
        learner.status = data["status"]
        if budget is not None:
            learner.budget = learner.used + budget
        return learner
