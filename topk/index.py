"""Top-K suggestions over a compressed prefix tree (radix tree).

Design notes
------------
* Keys are case-folded words; display aliases are aggregated per normalized
  word. Ranking is a total order: ``(-freq, normalized_word)`` -- frequency
  descending, then lexicographic ascending as the stable tie-break.
* Every radix node caches the top ``topk_capacity`` candidates of its
  subtree, stamped with a version. Mutations only mark the path to the root
  dirty; caches are recomputed lazily by merging the (already sorted) child
  caches, so frequency decreases and deletions automatically backfill the
  next-best candidates from the children without scanning the subtree.
* Queries first try the cached top-K of the prefix node (O(K)); if the page
  needed is deeper than the cache, a best-first traversal uses the cached
  per-node maxima as upper bounds to prune subtrees that cannot compete, so
  the whole prefix subtree is never scanned.
* Nodes live in an arena. ``compact()`` rebuilds the arena (reclaiming
  abandoned slots) and bumps the generation. Cursors opened before a
  compaction either keep reading the old version (default) or fail loudly
  with :class:`StaleCursorError` (``strict=True``).
"""

from __future__ import annotations

import bisect
import heapq
import itertools
from dataclasses import dataclass, field
from typing import Iterator, NamedTuple, Optional

__all__ = ["TopKIndex", "Suggestion", "Cursor", "StaleCursorError", "normalize"]


def normalize(text: str) -> str:
    """Canonical word identity: Unicode case folding."""
    return text.casefold()


class StaleCursorError(RuntimeError):
    """A strict cursor observed mutation or compaction of its pinned version."""


class Suggestion(NamedTuple):
    word: str     # normalized identity
    display: str  # chosen display alias
    freq: int


@dataclass
class _Entry:
    word: str
    freq: int
    aliases: dict[str, int]
    slot: "_Slot"

    @property
    def key(self) -> tuple[int, str]:
        return (-self.freq, self.word)

    @property
    def display(self) -> str:
        # Most frequent alias wins; ties break by case-folded then raw
        # lexicographic order, so the choice is total and deterministic.
        return min(self.aliases.items(), key=lambda kv: (-kv[1], kv[0].casefold(), kv[0]))[0]


class _Slot:
    """One radix-tree node living in the index arena."""

    __slots__ = (
        "label", "edges", "entry", "cache", "dirty", "version",
        "retired", "parent", "parent_key",
    )

    def __init__(self, label: str, parent: Optional["_Slot"], parent_key: Optional[str]):
        self.label = label
        self.edges: dict[str, _Slot] = {}
        self.entry: Optional[_Entry] = None
        self.cache: list[tuple[int, str]] = []
        self.dirty = True
        self.version = 0
        self.retired = False
        self.parent = parent
        self.parent_key = parent_key


class TopKIndex:
    def __init__(self, topk_capacity: int = 16):
        if topk_capacity < 1:
            raise ValueError("topk_capacity must be >= 1")
        self._capacity = topk_capacity
        self._arena: list[_Slot] = []
        self._root = self._new_slot("", None, None)
        self._words: dict[str, _Entry] = {}
        self._version = 0
        self._garbage = 0
        self.generation = 0

    # ------------------------------------------------------------------ arena

    def _new_slot(self, label: str, parent: Optional[_Slot], parent_key: Optional[str]) -> _Slot:
        slot = _Slot(label, parent, parent_key)
        self._arena.append(slot)
        return slot

    def stats(self) -> dict[str, int]:
        return {
            "version": self._version,
            "generation": self.generation,
            "arena_slots": len(self._arena),
            "live_slots": len(self._arena) - self._garbage,
            "garbage_slots": self._garbage,
            "words": len(self._words),
        }

    # -------------------------------------------------------------- mutation

    def _touch(self, slot: _Slot) -> None:
        self._version += 1
        node: Optional[_Slot] = slot
        while node is not None:
            node.dirty = True
            node.version = self._version
            node = node.parent

    def _descend(self, word: str) -> _Slot:
        """Find or create the terminal slot for ``word`` (radix insert)."""
        node = self._root
        i, n = 0, len(word)
        while i < n:
            key = word[i]
            child = node.edges.get(key)
            if child is None:
                leaf = self._new_slot(word[i:], node, key)
                node.edges[key] = leaf
                return leaf
            label = child.label
            j, m = 0, min(len(label), n - i)
            while j < m and label[j] == word[i + j]:
                j += 1
            if j == len(label):
                node = child
                i += j
                continue
            mid = self._new_slot(label[:j], node, key)
            node.edges[key] = mid
            child.label = label[j:]
            child.parent = mid
            child.parent_key = label[j]
            mid.edges[label[j]] = child
            if i + j == n:
                return mid
            leaf_key = word[i + j]
            leaf = self._new_slot(word[i + j:], mid, leaf_key)
            mid.edges[leaf_key] = leaf
            return leaf
        return node

    def add(self, word: str, delta: int = 1, display: Optional[str] = None) -> None:
        """Add ``delta`` occurrences of ``word`` (upsert)."""
        if delta <= 0:
            raise ValueError("delta must be positive")
        norm = normalize(word)
        if not norm:
            raise ValueError("empty word")
        entry = self._words.get(norm)
        if entry is None:
            slot = self._descend(norm)
            entry = _Entry(norm, 0, {}, slot)
            slot.entry = entry
            self._words[norm] = entry
        entry.freq += delta
        alias = word if display is None else display
        entry.aliases[alias] = entry.aliases.get(alias, 0) + delta
        self._touch(entry.slot)

    def set_freq(self, word: str, freq: int, display: Optional[str] = None) -> None:
        """Set the absolute frequency; ``freq <= 0`` removes the word."""
        norm = normalize(word)
        if freq <= 0:
            self.remove(word)
            return
        entry = self._words.get(norm)
        if entry is None:
            slot = self._descend(norm)
            entry = _Entry(norm, 0, {}, slot)
            slot.entry = entry
            self._words[norm] = entry
        entry.freq = freq
        alias = word if display is None else display
        entry.aliases[alias] = entry.aliases.get(alias, 0) + 1
        self._touch(entry.slot)

    def remove(self, word: str) -> bool:
        norm = normalize(word)
        entry = self._words.pop(norm, None)
        if entry is None:
            return False
        slot = entry.slot
        slot.entry = None
        self._touch(slot)
        self._compress_up(slot)
        return True

    def _compress_up(self, slot: _Slot) -> None:
        node = slot
        while node is not self._root and node.entry is None and len(node.edges) <= 1:
            parent = node.parent
            if not node.edges:
                del parent.edges[node.parent_key]
                self._garbage += 1
                node = parent
            else:
                child = next(iter(node.edges.values()))
                child.label = node.label + child.label
                child.parent = parent
                child.parent_key = node.parent_key
                parent.edges[node.parent_key] = child
                self._garbage += 1
                node = parent

    # ------------------------------------------------------------ cache build

    def _ensure_fresh(self, slot: _Slot) -> None:
        """Recompute a dirty node's versioned top-K bound from its children."""
        if not slot.dirty:
            return
        slot.dirty = False
        parts: list[list[tuple[int, str]]] = []
        if slot.entry is not None:
            parts.append([slot.entry.key])
        for child in slot.edges.values():
            self._ensure_fresh(child)
            parts.append(child.cache)
        if parts:
            slot.cache = list(itertools.islice(heapq.merge(*parts), self._capacity))
        else:
            slot.cache = []

    # ---------------------------------------------------------------- query

    def _locate(self, prefix: str) -> Optional[_Slot]:
        node = self._root
        i, n = 0, len(prefix)
        while i < n:
            child = node.edges.get(prefix[i])
            if child is None:
                return None
            label = child.label
            j, m = 0, min(len(label), n - i)
            while j < m and label[j] == prefix[i + j]:
                j += 1
            if j < len(label):
                # Prefix ends mid-edge: the whole child subtree matches,
                # otherwise the edge mismatches.
                return child if i + j == n else None
            node = child
            i += j
        return node

    def suggest(
        self,
        prefix: str = "",
        limit: int = 10,
        after: Optional[tuple[int, str]] = None,
    ) -> list[Suggestion]:
        """Top ``limit`` suggestions for ``prefix``, ranked by ``(-freq, word)``.

        ``after`` is an opaque pagination cursor: the ``(freq, word)`` pair of
        the last suggestion of the previous page. Results are strictly after
        that rank. The empty prefix is allowed and is bounded by ``limit``.
        """
        if limit < 0:
            raise ValueError("limit must be >= 0")
        if limit == 0:
            return []
        start = self._locate(normalize(prefix))
        if start is None:
            return []
        self._ensure_fresh(start)
        items = start.cache
        after_key = (-after[0], after[1]) if after is not None else None
        i = bisect.bisect_right(items, after_key) if after_key is not None else 0
        if len(items) < self._capacity or i + limit <= len(items):
            # The cache provably contains the whole requested page.
            return [self._to_suggestion(word) for (_, word) in items[i:i + limit]]
        return self._best_first(start, limit, after_key)

    def _best_first(
        self,
        start: _Slot,
        limit: int,
        after_key: Optional[tuple[int, str]],
    ) -> list[Suggestion]:
        """Best-first descent pruned by the versioned per-node upper bounds."""
        counter = itertools.count()
        heap: list[tuple[tuple[int, str], int, int, object]] = []

        def expand(slot: _Slot) -> None:
            if slot.entry is not None:
                push_item(slot.entry.key)
            for child in slot.edges.values():
                push_node(child)

        def push_node(slot: _Slot) -> None:
            self._ensure_fresh(slot)
            cache = slot.cache
            if not cache:
                return
            if after_key is None:
                heapq.heappush(heap, (cache[0], next(counter), 0, slot))
                return
            i = bisect.bisect_right(cache, after_key)
            if i < len(cache):
                # Frontier key: the best cached candidate past the cursor.
                heapq.heappush(heap, (cache[i], next(counter), 0, slot))
            elif len(cache) < self._capacity:
                return  # complete cache: nothing here ranks past the cursor
            else:
                # Full cache entirely at/below the cursor: the next
                # candidate hides deeper, expand inline to surface it.
                expand(slot)

        def push_item(key: tuple[int, str]) -> None:
            if after_key is not None and key <= after_key:
                return
            heapq.heappush(heap, (key, next(counter), 1, key))

        push_node(start)
        out: list[tuple[int, str]] = []
        while heap and len(out) < limit:
            key, _, kind, payload = heapq.heappop(heap)
            if kind == 1:
                out.append(key)  # type: ignore[arg-type]
            else:
                expand(payload)  # type: ignore[arg-type]
        return [self._to_suggestion(word) for (_, word) in out]

    def _to_suggestion(self, word: str, freq_hint: Optional[int] = None) -> Suggestion:
        entry = self._words.get(word)
        if entry is None:
            # Only reachable when reading a retired (old-version) snapshot.
            return Suggestion(word, word, freq_hint if freq_hint is not None else 0)
        return Suggestion(word, entry.display, entry.freq)

    # --------------------------------------------------------------- cursors

    def open_cursor(self, prefix: str = "", strict: bool = False) -> "Cursor":
        return Cursor(self, prefix, strict)

    # ------------------------------------------------------------- compaction

    def compact(self) -> None:
        """Rebuild the arena, reclaiming slots abandoned by deletes/splits.

        Old slots are frozen (``retired``) but kept alive as long as a cursor
        references them, so pre-compaction cursors keep reading the old
        version; strict cursors fail instead.
        """
        self._ensure_fresh(self._root)
        old_arena = self._arena
        self._arena = []
        self.generation += 1
        self._root = self._copy_subtree(self._root, None, None)
        for slot in old_arena:
            slot.retired = True
        self._garbage = 0

    def _copy_subtree(self, old: _Slot, parent: Optional[_Slot], parent_key: Optional[str]) -> _Slot:
        new = self._new_slot(old.label, parent, parent_key)
        new.entry = old.entry
        if old.entry is not None:
            old.entry.slot = new
        new.cache = list(old.cache)
        new.dirty = False
        new.version = old.version
        for key, child in old.edges.items():
            new.edges[key] = self._copy_subtree(child, new, key)
        return new

    # -------------------------------------------------------------- documents

    def load_text(self, text: str) -> int:
        """Tokenize local UTF-8 text (see ``topk.tokenizer``) and add tokens."""
        from .tokenizer import tokenize

        count = 0
        for token in tokenize(text):
            self.add(token.display)
            count += 1
        return count

    def __contains__(self, word: str) -> bool:
        return normalize(word) in self._words

    def freq(self, word: str) -> int:
        entry = self._words.get(normalize(word))
        return entry.freq if entry is not None else 0


class Cursor:
    """A pinned reader over the top-K of one prefix.

    * Normal mutations: a non-strict cursor transparently re-reads the new
      version; a strict cursor raises :class:`StaleCursorError`.
    * Compaction (arena relocation): a non-strict cursor keeps reading the
      frozen old version; a strict cursor raises :class:`StaleCursorError`.

    ``read`` limits are clamped to the index ``topk_capacity``.
    """

    def __init__(self, index: TopKIndex, prefix: str, strict: bool = False):
        self._index = index
        self._strict = strict
        slot = index._locate(normalize(prefix))
        self._slot = slot
        self._version = slot.version if slot is not None else None
        self._generation = index.generation

    @property
    def stale(self) -> bool:
        return self._slot is not None and self._slot.retired

    def read(self, limit: int = 10) -> list[Suggestion]:
        index = self._index
        slot = self._slot
        if slot is None:
            return []
        limit = min(limit, index._capacity)
        if slot.retired:
            if self._strict:
                raise StaleCursorError(
                    "cursor pinned generation %d, index is at generation %d"
                    % (self._generation, index.generation)
                )
            # Old-version read: the retired slot was frozen by compact().
            out = []
            for negfreq, word in slot.cache[:limit]:
                entry = index._words.get(word)
                display = entry.display if entry is not None else word
                out.append(Suggestion(word, display, -negfreq))
            return out
        if self._strict and (
            slot.dirty
            or slot.version != self._version
            or self._generation != index.generation
        ):
            raise StaleCursorError("pinned version was mutated")
        index._ensure_fresh(slot)
        self._version = slot.version
        self._generation = index.generation
        return [index._to_suggestion(word) for (_, word) in slot.cache[:limit]]
