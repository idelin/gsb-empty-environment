"""Independently executable counterexamples.

Each case first exhibits a concrete failure of a *naive* implementation
strategy, then shows that the minimal automaton in dawg.py survives the
same scenario.

Run:  python3 counterexamples.py
Exit code 0 iff every check holds; 1 otherwise.
"""

import sys

from dawg import MinimalWordAutomaton


class NaiveTrie:
    """Deliberately naive trie: deletion prunes the last edge outright."""

    END = "\x00"

    def __init__(self):
        self.root = {}

    def add(self, word):
        node = self.root
        for ch in word:
            node = node.setdefault(ch, {})
        node[self.END] = True

    def contains(self, word):
        node = self.root
        for ch in word:
            if ch not in node:
                return False
            node = node[ch]
        return self.END in node

    def remove_buggy(self, word):
        node = self.root
        for ch in word[:-1]:
            node = node[ch]
        del node[word[-1]]  # bug: drops the whole subtree below the last edge


class _MutableState:
    """Shared mutable state as found in naive in-place DAWG minimization."""

    def __init__(self, final=False):
        self.final = final
        self.children = {}


def _accepts(root, word):
    node = root
    for ch in word:
        node = node.children.get(ch)
        if node is None:
            return False
    return node.final


def case_delete_prefix_word_keeps_longer():
    naive = NaiveTrie()
    naive.add("abc")
    naive.add("abcd")
    naive.remove_buggy("abc")
    naive_broken = not naive.contains("abcd")

    aut = MinimalWordAutomaton("abcd")
    aut.add("abc")
    aut.add("abcd")
    aut.remove("abc")
    ours_ok = not aut.contains("abc") and aut.contains("abcd")
    return naive_broken and ours_ok


def case_shared_suffix_copy_on_write():
    shared_c = _MutableState(final=True)
    shared_b = _MutableState()
    shared_b.children["c"] = shared_c
    root = _MutableState()
    root.children["a"] = shared_b
    root.children["x"] = shared_b  # "abc" and "xbc" share the "bc" suffix
    del shared_b.children["c"]     # bug: in-place edit pollutes the other word
    naive_broken = not _accepts(root, "xbc")

    aut = MinimalWordAutomaton("abcx")
    aut.add("abc")
    aut.add("xbc")
    aut.remove("abc")
    ours_ok = aut.contains("xbc") and not aut.contains("abc")
    return naive_broken and ours_ok


def case_duplicate_adds_need_counts():
    naive = NaiveTrie()
    naive.add("ab")
    naive.add("ab")
    naive.remove_buggy("ab")
    naive_broken = naive.contains("ab") is False  # one delete erased both adds

    from dictionary import FrequencyDictionary
    d = FrequencyDictionary("ab")
    d.add("ab")
    d.add("ab")
    d.remove("ab")
    ours_ok = d.contains("ab") and d.count("ab") == 1
    d.remove("ab")
    ours_ok = ours_ok and not d.contains("ab")
    return naive_broken and ours_ok


def case_snapshot_is_persistent():
    aut = MinimalWordAutomaton("abcd")
    aut.add("abc")
    snap = aut.snapshot()
    aut.remove("abc")
    aut.add("abd")
    return snap.contains("abc") and not snap.contains("abd") and not aut.contains("abc")


def case_minimality_state_sharing():
    aut = MinimalWordAutomaton("abcx")
    aut.add("abc")
    aut.add("xbc")
    # minimal DFA for {"abc","xbc"}: root, merged "b"-state, "c"-state, final leaf
    return aut.state_count == 4


CASES = [
    ("delete prefix word keeps longer words", case_delete_prefix_word_keeps_longer),
    ("shared suffix copy-on-write", case_shared_suffix_copy_on_write),
    ("duplicate adds counted, not set-like", case_duplicate_adds_need_counts),
    ("snapshot survives later mutations", case_snapshot_is_persistent),
    ("minimality: suffix states merged", case_minimality_state_sharing),
]


def main():
    failed = 0
    for name, fn in CASES:
        ok = fn()
        print(f"[{'PASS' if ok else 'FAIL'}] {name}")
        failed += 0 if ok else 1
    print(f"{len(CASES) - failed}/{len(CASES)} counterexample checks passed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
