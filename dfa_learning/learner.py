"""Observation-table learner (L*-style) with a query budget and checkpoints.

The learner maintains a closed, consistent observation table (S, E, T) and
builds an executable hypothesis DFA from it.  It only interacts with the
target through a membership oracle and a bounded equivalence oracle.

Key properties:

* Unknown table cells are stored as None and are never treated as rejection.
* Membership answers are cached; resumed runs reuse the cache instead of
  re-asking the oracle.
* A counterexample is processed by adding its distinguishing suffixes to E
  one at a time, so the position inside counterexample processing survives
  a budget stop and is restored from the checkpoint.
* Checkpoints are bound to the (ordered) alphabet, the domain bound L and
  the oracle version; mismatched evidence is rejected on load.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field

from .dfa import DFA
from .oracles import DomainLimitError

EQUIVALENT_IN_DOMAIN = "equivalent_in_domain"
BUDGET_EXHAUSTED = "budget_exhausted"
HYPOTHESIS_READY = "hypothesis_ready"


class CheckpointMismatchError(Exception):
    """A checkpoint does not match the alphabet, L, or oracle version in use."""


@dataclass
class LearnResult:
    """Outcome of a learning run.

    ``status`` is one of:

    * ``equivalent_in_domain`` -- the bounded equivalence oracle found no
      counterexample; the hypothesis agrees with the hidden machine on every
      word of length <= L.  Nothing is claimed about longer words.
    * ``budget_exhausted`` -- undecided; the query budget ran out.  Save a
      checkpoint and resume with a larger budget.
    * ``hypothesis_ready`` -- an executable hypothesis was built but no
      equivalence oracle was supplied.
    """

    status: str
    hypothesis: DFA | None
    rounds: list = field(default_factory=list)
    membership_queries: int = 0
    cache_hits: int = 0
    equivalence_queries: int = 0

    @property
    def decided(self):
        return self.status == EQUIVALENT_IN_DOMAIN


class Learner:
    """L*-style learner over the bounded domain of words of length <= L."""

    FORMAT = "dfa-learning-checkpoint"
    FORMAT_VERSION = 1

    def __init__(self, membership_oracle, alphabet, max_len, budget):
        self.oracle = membership_oracle
        self.alphabet = tuple(alphabet)
        self.max_len = max_len
        self.budget = budget  # total number of *new* membership queries allowed

        self.S = [""]          # access prefixes (rows), prefix-closed
        self.E = [""]          # distinguishing suffixes (columns)
        self.T = {}            # (s, e) -> True / False / None (unknown)
        self.cache = {}        # word -> bool, every membership answer so far
        self.pending_suffixes = []  # suffixes of a counterexample not yet in E

        self.membership_queries = 0   # new (uncached) membership queries used
        self.cache_hits = 0
        self.equivalence_queries = 0
        self.rounds = []              # one record per equivalence query
        self.current_hypothesis = None

        self._mq_mark = 0
        self._hit_mark = 0

    # ------------------------------------------------------------------
    # membership queries, cache, table cells
    # ------------------------------------------------------------------

    def mq(self, word):
        """Membership query with cache and budget.

        Returns True/False, or None when the budget is exhausted.  Raises
        DomainLimitError for words outside the query domain; in particular
        unknown cells are never silently treated as rejection.
        """
        if len(word) > self.max_len:
            raise DomainLimitError(
                f"word {word!r} exceeds the domain bound L={self.max_len}"
            )
        if word in self.cache:
            self.cache_hits += 1
            return self.cache[word]
        if self.membership_queries >= self.budget:
            return None
        value = bool(self.oracle.query(word))
        self.membership_queries += 1
        self.cache[word] = value
        return value

    def _cell(self, s, e):
        key = (s, e)
        value = self.T.get(key)
        if value is None:
            value = self.mq(s + e)
            if value is None:
                self.T[key] = None  # remember that this cell is still unknown
                return None
            self.T[key] = value
        return value

    def _rows(self):
        rows = list(self.S)
        for s in self.S:
            for symbol in self.alphabet:
                rows.append(s + symbol)
        return rows

    def _fill(self):
        """Try to determine every unknown cell; False if the budget ran out."""
        for s in self._rows():
            for e in self.E:
                if self._cell(s, e) is None:
                    return False
        return True

    def _row_tuple(self, s):
        return tuple(self.T[(s, e)] for e in self.E)

    # ------------------------------------------------------------------
    # closure, consistency, hypothesis
    # ------------------------------------------------------------------

    def _make_closed_and_consistent(self):
        """False if the budget ran out mid-way (position kept in the table)."""
        while True:
            if not self._fill():
                return False
            unclosed = self._find_unclosed_row()
            if unclosed is not None:
                self.S.append(unclosed)
                continue
            separator = self._find_inconsistency()
            if separator is not None:
                self.E.append(separator)
                continue
            return True

    def _find_unclosed_row(self):
        rowset = {self._row_tuple(s) for s in self.S}
        for s in self.S:
            for symbol in self.alphabet:
                t = s + symbol
                if self._row_tuple(t) not in rowset:
                    return t
        return None

    def _find_inconsistency(self):
        for i in range(len(self.S)):
            for j in range(i + 1, len(self.S)):
                if self._row_tuple(self.S[i]) != self._row_tuple(self.S[j]):
                    continue
                for symbol in self.alphabet:
                    row_i = self._row_tuple(self.S[i] + symbol)
                    row_j = self._row_tuple(self.S[j] + symbol)
                    for k, e in enumerate(self.E):
                        if row_i[k] != row_j[k]:
                            return symbol + e
        return None

    def _build_hypothesis(self):
        """Build an executable DFA from the closed, consistent table."""
        index = {}
        states = []
        for s in self.S:
            row = self._row_tuple(s)
            if row not in index:
                index[row] = len(states)
                states.append(row)
        transitions = {}
        for s in self.S:
            source = index[self._row_tuple(s)]
            for symbol in self.alphabet:
                transitions[(source, symbol)] = index[self._row_tuple(s + symbol)]
        accepting = {
            index[self._row_tuple(s)] for s in self.S if self.T[(s, "")]
        }
        return DFA(
            states=range(len(states)),
            alphabet=self.alphabet,
            start=index[self._row_tuple("")],
            transitions=transitions,
            accepting=accepting,
        )

    # ------------------------------------------------------------------
    # counterexamples
    # ------------------------------------------------------------------

    def _begin_counterexample(self, word):
        """Queue every suffix of the counterexample that is not yet a column."""
        suffixes = [word[i:] for i in range(len(word) + 1)]
        queued = set(self.E) | set(self.pending_suffixes)
        self.pending_suffixes.extend(s for s in suffixes if s not in queued)

    def _process_pending_suffixes(self):
        """Add queued distinguishing suffixes; False if the budget ran out."""
        while self.pending_suffixes:
            suffix = self.pending_suffixes.pop(0)
            if suffix not in self.E:
                self.E.append(suffix)
            for s in self._rows():
                if self._cell(s, suffix) is None:
                    return False
        return True

    def submit_counterexample(self, word):
        """Offer an external counterexample.

        The word is first verified with a membership query: it is accepted
        only if the current hypothesis and the oracle disagree on it.  A
        wrong (or out-of-domain, or unverifiable) counterexample is rejected
        and the table is left untouched.
        """
        if len(word) > self.max_len:
            return False
        if self.current_hypothesis is None:
            if not self._process_pending_suffixes():
                return False
            if not self._make_closed_and_consistent():
                return False
            self.current_hypothesis = self._build_hypothesis()
        actual = self.mq(word)
        if actual is None:
            return False  # cannot verify within the remaining budget
        if self.current_hypothesis.accepts(word) == actual:
            return False  # wrong counterexample: rejected, table not polluted
        self._begin_counterexample(word)
        # Fold the new columns in immediately when the budget allows; any
        # remainder stays queued and is finished by the next learn() call.
        self._process_pending_suffixes()
        return True

    # ------------------------------------------------------------------
    # main loop
    # ------------------------------------------------------------------

    def learn(self, equivalence_oracle=None):
        """Run until domain equivalence, a ready hypothesis, or budget stop."""
        if equivalence_oracle is not None:
            if equivalence_oracle.version != self.oracle.version:
                raise CheckpointMismatchError(
                    "membership and equivalence oracles have different versions"
                )
        while True:
            if not self._process_pending_suffixes():
                return self._result(BUDGET_EXHAUSTED)
            if not self._make_closed_and_consistent():
                return self._result(BUDGET_EXHAUSTED)
            hypothesis = self._build_hypothesis()
            self.current_hypothesis = hypothesis
            if equivalence_oracle is None:
                return self._result(HYPOTHESIS_READY, hypothesis)
            self.equivalence_queries += 1
            counterexample = equivalence_oracle.find_counterexample(hypothesis)
            self._record_round(hypothesis, counterexample)
            if counterexample is None:
                return self._result(EQUIVALENT_IN_DOMAIN, hypothesis)
            self._begin_counterexample(counterexample)

    def _record_round(self, hypothesis, counterexample):
        self.rounds.append(
            {
                "index": len(self.rounds),
                "states": hypothesis.num_states,
                "counterexample": counterexample,
                "new_membership_queries": self.membership_queries - self._mq_mark,
                "cache_hits": self.cache_hits - self._hit_mark,
            }
        )
        self._mq_mark = self.membership_queries
        self._hit_mark = self.cache_hits

    def _result(self, status, hypothesis=None):
        return LearnResult(
            status=status,
            hypothesis=hypothesis,
            rounds=list(self.rounds),
            membership_queries=self.membership_queries,
            cache_hits=self.cache_hits,
            equivalence_queries=self.equivalence_queries,
        )

    # ------------------------------------------------------------------
    # checkpoints
    # ------------------------------------------------------------------

    def save_checkpoint(self, path):
        """Persist table, counterexample position, cache and counters."""
        data = {
            "format": self.FORMAT,
            "format_version": self.FORMAT_VERSION,
            "binding": {
                "alphabet": list(self.alphabet),
                "max_len": self.max_len,
                "oracle_version": self.oracle.version,
            },
            "budget": self.budget,
            "S": self.S,
            "E": self.E,
            "T": [[s, e, v] for (s, e), v in self.T.items()],
            "cache": self.cache,
            "pending_suffixes": self.pending_suffixes,
            "counters": {
                "membership_queries": self.membership_queries,
                "cache_hits": self.cache_hits,
                "equivalence_queries": self.equivalence_queries,
                "mq_mark": self._mq_mark,
                "hit_mark": self._hit_mark,
            },
            "rounds": self.rounds,
        }
        with open(path, "w", encoding="utf-8") as handle:
            json.dump(data, handle, indent=2)

    @classmethod
    def load_checkpoint(cls, path, membership_oracle, *, alphabet=None,
                        max_len=None, budget=None):
        """Restore a learner, rejecting evidence bound to other parameters."""
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
        if data.get("format") != cls.FORMAT or \
                data.get("format_version") != cls.FORMAT_VERSION:
            raise CheckpointMismatchError("unrecognised checkpoint format")
        binding = data["binding"]
        if binding["oracle_version"] != membership_oracle.version:
            raise CheckpointMismatchError(
                f"checkpoint is bound to oracle version "
                f"{binding['oracle_version']!r}, current oracle is "
                f"{membership_oracle.version!r}; old evidence rejected"
            )
        if alphabet is not None and tuple(alphabet) != tuple(binding["alphabet"]):
            raise CheckpointMismatchError(
                "checkpoint alphabet (including its order) does not match"
            )
        if max_len is not None and max_len != binding["max_len"]:
            raise CheckpointMismatchError("checkpoint domain bound L does not match")

        learner = cls(
            membership_oracle,
            alphabet=binding["alphabet"],
            max_len=binding["max_len"],
            budget=budget if budget is not None else data["budget"],
        )
        learner.S = list(data["S"])
        learner.E = list(data["E"])
        learner.T = {(s, e): v for s, e, v in data["T"]}
        learner.cache = {word: bool(value) for word, value in data["cache"].items()}
        learner.pending_suffixes = list(data["pending_suffixes"])
        counters = data["counters"]
        learner.membership_queries = counters["membership_queries"]
        learner.cache_hits = counters["cache_hits"]
        learner.equivalence_queries = counters["equivalence_queries"]
        learner.rounds = list(data["rounds"])
        learner._mq_mark = counters.get("mq_mark", learner.membership_queries)
        learner._hit_mark = counters.get("hit_mark", learner.cache_hits)
        return learner
