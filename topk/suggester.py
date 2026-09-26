"""TopK 建议器：压缩前缀树 + 带版本 TopK 上界缓存 + 排名/字典序分页。

排名规则（稳定决胜）：频次降序，频次相同按规范词字典序升序。
显示形式：同一规范词（NFC + casefold）的多个大小写/编码别名按观测
频次聚合，显示出现次数最多的别名；并列时取字典序最小者，保证确定性。

游标与内存压缩：
* 每次 ``compact()``（内存压缩重定位，重建全部节点存储）使 ``generation`` +1。
* 分页游标绑定 ``(generation, 规范化前缀, 已消费位次, 末条排名键)``。
* 采用"明确失效"策略：重定位后旧游标再使用会抛出 ``StaleCursorError``，
  调用方应重新发起第一页查询。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Optional

from .tokenizer import normalize, tokenize
from .trie import RadixTrie

__all__ = [
    "StaleCursorError",
    "Suggestion",
    "Cursor",
    "Page",
    "TopKSuggester",
]


class StaleCursorError(RuntimeError):
    """游标对应的数据版本已被内存压缩重定位替换，游标明确失效。"""


@dataclass(frozen=True)
class Suggestion:
    display: str      # 显示形式（观测频次最高的别名）
    normalized: str   # 规范词身份
    freq: int         # 聚合频次


@dataclass(frozen=True)
class Cursor:
    """排名分页游标（不透明，跨页原样回传）。"""

    generation: int
    prefix: str       # 规范化前缀
    position: int     # 已消费条数
    last_freq: int    # 末条排名键
    last_word: str


@dataclass(frozen=True)
class Page:
    items: list[Suggestion]
    cursor: Optional[Cursor]   # 有下一页时为非 None
    has_more: bool


class TopKSuggester:
    def __init__(self, base_bound: int = 16, max_results: int = 1000) -> None:
        self._freq: dict[str, int] = {}
        self._aliases: dict[str, dict[str, int]] = {}
        self._display: dict[str, str] = {}
        self._trie = RadixTrie(self._freq.get, base_bound=base_bound)
        self._generation = 0
        self.max_results = max_results

    # ------------------------------------------------------------------
    # 写入
    # ------------------------------------------------------------------
    def add(self, word: str, count: int = 1) -> None:
        """观测 word 出现 count 次（同时记录该表面形式别名）。"""
        if count <= 0:
            raise ValueError("count must be positive")
        norm = normalize(word)
        if not norm:
            return
        self._observe_alias(norm, word, count)
        self._set_freq(norm, self._freq.get(norm, 0) + count)

    def set(self, word: str, count: int) -> None:
        """直接设定词频；count <= 0 等价于删除。别名至少登记一次。"""
        norm = normalize(word)
        if not norm:
            return
        if count <= 0:
            self.remove(word)
            return
        self._observe_alias(norm, word, 0)
        self._set_freq(norm, count)

    def remove(self, word: str) -> bool:
        norm = normalize(word)
        if norm not in self._freq:
            return False
        del self._freq[norm]
        self._aliases.pop(norm, None)
        self._display.pop(norm, None)
        self._trie.delete(norm)
        return True

    def load_text(self, text: str) -> int:
        """按 tokenizer 规则切分本地 UTF-8 文本并累计词频，返回词数。"""
        n = 0
        for token in tokenize(text):
            self.add(token, 1)
            n += 1
        return n

    # ------------------------------------------------------------------
    # 查询
    # ------------------------------------------------------------------
    def suggest(self, prefix: str = "", k: int = 10, cursor: Optional[Cursor] = None) -> Page:
        """按 (频次降序, 规范词升序) 返回前缀匹配的前 k 条。

        空前缀即全局 TopK，结果条数受 max_results 上限约束。
        利用节点 TopK 上界缓存，只刷新失效路径，不扫描整个前缀子树。
        """
        k = max(0, min(k, self.max_results))
        norm_prefix = normalize(prefix)
        if cursor is not None:
            if cursor.generation != self._generation:
                raise StaleCursorError(
                    "cursor invalidated by memory compaction (generation "
                    f"{cursor.generation} != {self._generation})"
                )
            if cursor.prefix != norm_prefix:
                raise ValueError("cursor belongs to a different prefix")
        located = self._trie.locate(norm_prefix)
        if located is None or k == 0:
            return Page([], None, False)
        node, _ = located
        position = cursor.position if cursor else 0
        # 保证缓存容量覆盖 已消费 + 本页，触发按需重建与候选补回
        self._trie.ensure(node, position + k)
        entries = node.cache
        if cursor is not None:
            cut = (-cursor.last_freq, cursor.last_word)
            entries = [e for e in entries if (-e[1], e[0]) > cut]
        page = entries[:k]
        consumed = position + len(page)
        has_more = consumed < node.total
        next_cursor = None
        if page and has_more:
            last_word, last_freq = page[-1]
            next_cursor = Cursor(self._generation, norm_prefix, consumed, last_freq, last_word)
        return Page([self._make(w, f) for w, f in page], next_cursor, has_more)

    def list_lex(
        self,
        prefix: str = "",
        limit: int = 10,
        start_after: Optional[str] = None,
    ) -> tuple[list[Suggestion], Optional[str]]:
        """字典序分页：返回 (> start_after) 的前 limit 条及下一页起点。"""
        limit = max(0, min(limit, self.max_results))
        norm_prefix = normalize(prefix)
        norm_after = normalize(start_after) if start_after else None
        words = self._trie.iter_lex(norm_prefix, norm_after, limit + 1)
        has_more = len(words) > limit
        page = words[:limit]
        next_after = page[-1] if page and has_more else None
        return [self._make(w, self._freq[w]) for w in page], next_after

    def frequency(self, word: str) -> int:
        return self._freq.get(normalize(word), 0)

    def display_of(self, word: str) -> Optional[str]:
        return self._display.get(normalize(word))

    def __len__(self) -> int:
        return len(self._freq)

    # ------------------------------------------------------------------
    # 内存压缩重定位
    # ------------------------------------------------------------------
    @property
    def generation(self) -> int:
        return self._generation

    @property
    def trie_version(self) -> int:
        return self._trie.version

    def compact(self) -> None:
        """内存压缩重定位：把全部词重建进全新的节点存储，丢弃旧缓存。

        旧节点整体被替换（逻辑重定位），``generation`` 递增；
        此前发放的分页游标明确失效（再使用抛 ``StaleCursorError``）。
        """
        new_trie = RadixTrie(self._freq.get, base_bound=self._trie.base_bound)
        for norm in sorted(self._freq):
            new_trie.upsert(norm, self._freq[norm])
        self._trie = new_trie
        self._generation += 1

    def stats(self) -> dict[str, int]:
        return {
            "words": len(self._freq),
            "nodes": self._trie.node_count(),
            "trie_version": self._trie.version,
            "generation": self._generation,
            "refreshed_nodes": self._trie.refresh_count,
        }

    # ------------------------------------------------------------------
    # 内部
    # ------------------------------------------------------------------
    def _set_freq(self, norm: str, new_freq: int) -> None:
        old = self._freq.get(norm, 0)
        if new_freq == old:
            return
        self._freq[norm] = new_freq
        if new_freq > old:
            self._trie.upsert(norm, new_freq)
        else:
            self._trie.decrease(norm)

    def _observe_alias(self, norm: str, surface: str, count: int) -> None:
        aliases = self._aliases.setdefault(norm, {})
        aliases[surface] = aliases.get(surface, 0) + count
        best = min(aliases.items(), key=lambda kv: (-kv[1], kv[0]))
        self._display[norm] = best[0]

    def _make(self, norm: str, freq: int) -> Suggestion:
        return Suggestion(display=self._display.get(norm, norm), normalized=norm, freq=freq)
