"""Minimal acyclic deterministic finite automaton for a finite word set.

The automaton is maintained online: every insertion/deletion rebuilds only
the root-to-target path (copy-on-write) and re-canonicalizes nodes through a
hash-consing registry, so equivalent states are always merged and the
automaton stays minimal after every mutation. See README.md for the proof.

Only the Python standard library is used. The alphabet and the transition
structure are explicit inputs; no regex or automata libraries are involved.
"""

from __future__ import annotations


class _Node:
    """Immutable automaton state. children: tuple of (char, _Node), sorted."""

    __slots__ = ("final", "children")

    def __init__(self, final, children):
        self.final = final
        self.children = children

    def child(self, char):
        for c, node in self.children:
            if c == char:
                return node
        return None


def _is_dead_leaf(node):
    return not node.final and not node.children


def _set_child(children, char, new_child):
    """Return a new sorted children tuple with the edge to `char` replaced.

    An edge to a dead leaf (non-final, childless) is dropped instead.
    """
    result = []
    found = False
    for c, node in children:
        if c == char:
            found = True
            if not _is_dead_leaf(new_child):
                result.append((char, new_child))
        else:
            result.append((c, node))
    if not found and not _is_dead_leaf(new_child):
        result.append((char, new_child))
        result.sort(key=lambda pair: pair[0])
    return tuple(result)


class MinimalWordAutomaton:
    """Online minimal acyclic DFA over an explicitly given alphabet."""

    def __init__(self, alphabet):
        alphabet = frozenset(alphabet)
        if not alphabet:
            raise ValueError("alphabet must be non-empty")
        for ch in alphabet:
            if not isinstance(ch, str) or len(ch) != 1:
                raise ValueError("alphabet symbols must be single characters")
        self._alphabet = alphabet
        self._registry = {}
        self._root = self._intern(False, ())
        self._size = 0

    @property
    def alphabet(self):
        return self._alphabet

    @property
    def state_count(self):
        """Number of states reachable from the current root (minimal DFA size)."""
        seen = set()
        stack = [self._root]
        while stack:
            node = stack.pop()
            if id(node) in seen:
                continue
            seen.add(id(node))
            stack.extend(child for _, child in node.children)
        return len(seen)

    @property
    def registry_size(self):
        """Interned nodes including unreachable garbage (reclaimable by GC)."""
        return len(self._registry)

    def __len__(self):
        return self._size

    def _intern(self, final, children):
        key = (final, children)
        node = self._registry.get(key)
        if node is None:
            node = _Node(final, children)
            self._registry[key] = node
        return node

    def _dead_leaf(self):
        return self._registry[(False, ())]

    def _check_word(self, word):
        if not isinstance(word, str):
            raise TypeError("word must be a str")
        bad = sorted(set(word) - self._alphabet)
        if bad:
            raise ValueError(f"symbols not in alphabet: {bad!r}")

    def add(self, word):
        """Insert word; return True if the language changed."""
        self._check_word(word)
        if self.contains(word):
            return False
        self._root = self._insert(self._root, word, 0)
        self._size += 1
        return True

    def _insert(self, node, word, i):
        if i == len(word):
            return self._intern(True, node.children)
        char = word[i]
        child = node.child(char)
        if child is None:
            child = self._dead_leaf()
        new_child = self._insert(child, word, i + 1)
        return self._intern(node.final, _set_child(node.children, char, new_child))

    def remove(self, word):
        """Delete word; return True if the language changed."""
        self._check_word(word)
        if not self.contains(word):
            return False
        self._root = self._delete(self._root, word, 0)
        self._size -= 1
        return True

    def _delete(self, node, word, i):
        if i == len(word):
            return self._intern(False, node.children)
        char = word[i]
        child = node.child(char)
        new_child = self._delete(child, word, i + 1)
        return self._intern(node.final, _set_child(node.children, char, new_child))

    def _node_for(self, prefix):
        node = self._root
        for char in prefix:
            node = node.child(char)
            if node is None:
                return None
        return node

    def contains(self, word):
        node = self._node_for(word)
        return node is not None and node.final

    def __contains__(self, word):
        return self.contains(word)

    def has_prefix(self, prefix):
        return self._node_for(prefix) is not None

    def words_with_prefix(self, prefix):
        """All accepted words starting with prefix, in lexicographic order."""
        node = self._node_for(prefix)
        if node is None:
            return []
        result = []

        def walk(n, parts):
            if n.final:
                result.append("".join(parts))
            for c, child in n.children:
                parts.append(c)
                walk(child, parts)
                parts.pop()

        walk(node, list(prefix))
        return result

    def words(self):
        return self.words_with_prefix("")

    def snapshot(self):
        """Persistent read-only view of the current language.

        Because nodes are immutable and mutations are copy-on-write, a
        snapshot is O(1) and is never affected by later add/remove calls.
        """
        return _Snapshot(self._root)


class _Snapshot:
    __slots__ = ("_root",)

    def __init__(self, root):
        self._root = root

    def _node_for(self, prefix):
        node = self._root
        for char in prefix:
            node = node.child(char)
            if node is None:
                return None
        return node

    def contains(self, word):
        node = self._node_for(word)
        return node is not None and node.final

    def words(self):
        result = []

        def walk(n, parts):
            if n.final:
                result.append("".join(parts))
            for c, child in n.children:
                parts.append(c)
                walk(child, parts)
                parts.pop()

        walk(self._root, [])
        return result
