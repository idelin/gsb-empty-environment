"""匹配器：一次性匹配 + 分块流式匹配 + 带标签捕获恢复。

匹配语义（确定的优先规则）：
    1. 最左：从最小的起始位置匹配；
    2. 最长：在该起始位置取最长匹配终点（流式模式下因此需要“等待”）；
    3. 优先级：在达到该最长终点的所有路径中，按交替分支书写顺序、
       星号贪婪的优先级取第一条路径的捕获。

捕获恢复：DFA 只负责判定与定位最长终点（DFA 状态是 NFA 状态的合并，
不携带标签）；随后在原 NFA 上按优先级顺序做带备忘的深度优先搜索，
重走消费该区间的路径并收集标签事件。标签因此永远不会在状态合并时
丢失。空串 epsilon 环通过“进行中即失败”的备忘标记排除，嵌套星号
不会死循环。
"""
from __future__ import annotations

from .dfa import Determinizer
from .errors import BudgetExceeded
from .nfa import CHAR, EPS, TAG, build
from .parser import Parser


class Match:
    __slots__ = ("start", "end", "text", "captures")

    def __init__(self, start, end, text, captures):
        self.start = start
        self.end = end
        self.text = text
        self.captures = captures  # 组号 -> (start, end) | None

    def span(self, group=0):
        if group == 0:
            return (self.start, self.end)
        return self.captures.get(group)

    def group(self, group=0):
        sp = self.span(group)
        return None if sp is None else self._slice(sp)

    def _slice(self, sp):
        return self.text[sp[0] - self.start: sp[1] - self.start]

    def __repr__(self):
        return f"Match({self.start}, {self.end}, {self.text!r}, {self.captures})"


class SearchResult:
    __slots__ = ("matches", "incomplete")

    def __init__(self, matches, incomplete=False):
        self.matches = matches
        self.incomplete = incomplete  # True 表示超出资源预算，结果可能不完整

    def __iter__(self):
        return iter(self.matches)

    def __len__(self):
        return len(self.matches)


class Regex:
    def __init__(self, pattern, alphabet, max_states=4096, cache_size=4096):
        parser = Parser(pattern)
        self.pattern = pattern
        self.ast = parser.parse()
        self.group_count = parser.n_groups
        self.alphabet = frozenset(alphabet)
        self.nfa, self.start, self.accept = build(self.ast, self.alphabet)
        self.max_states = max_states
        self.cache_size = cache_size
        self._det = None

    def _determinizer(self):
        if self._det is None:
            self._det = Determinizer(
                self.nfa, self.start, self.accept, self.alphabet,
                max_states=self.max_states, cache_size=self.cache_size)
        return self._det

    def _check_text(self, text):
        bad = set(text) - self.alphabet
        if bad:
            raise ValueError(f"输入包含字母表外字符: {sorted(bad)!r}")

    # ---- 捕获恢复：在 NFA 上按优先级 DFS 重走 text[s:e] ----
    def _recover(self, text, s, e):
        nfa = self.nfa
        accept = self.accept
        memo = {}

        def dfs(state, p):
            key = (state, p)
            if key in memo:
                return memo[key]
            memo[key] = None  # 进行中：无消耗的 epsilon 环不可走
            best = None
            if p == e and state == accept:
                best = []
            else:
                for kind, dst, payload in nfa.edges[state]:
                    if kind is EPS:
                        r = dfs(dst, p)
                        if r is not None:
                            best = r
                            break
                    elif kind is TAG:
                        r = dfs(dst, p)
                        if r is not None:
                            best = [(payload, p)] + r
                            break
                    elif p < e and text[p] in payload:
                        r = dfs(dst, p + 1)
                        if r is not None:
                            best = r
                            break
            memo[key] = best
            return best

        events = dfs(self.start, s)
        assert events is not None, "DFA 已接受，NFA 路径必然存在"
        caps = {i: None for i in range(1, self.group_count + 1)}
        stacks = {}
        spans = []
        for (index, is_start), p in events:
            if is_start:
                stacks.setdefault(index, []).append(p)
            else:
                spans.append((index, stacks[index].pop(), p))
        for index, st, en in spans:  # 后写覆盖：循环组取最后一次迭代
            caps[index] = (st, en)
        return caps

    # ---- 一次性匹配 ----
    def _search(self, text, pos):
        det = self._determinizer()
        n = len(text)
        for s in range(pos, n + 1):
            d = det.start_id
            last = s if det.is_accept(d) else None
            p = s
            while p < n:
                d = det.step(d, text[p])  # 可能抛 BudgetExceeded
                p += 1
                if d is None:
                    break
                if det.is_accept(d):
                    last = p
            if last is not None:
                return Match(s, last, text[s:last], self._recover(text, s, last))
        return None

    def search(self, text, pos=0):
        """最左最长匹配；预算不足时抛 BudgetExceeded。"""
        self._check_text(text)
        return self._search(text, pos)

    def findall(self, text):
        """返回 SearchResult；预算不足时 incomplete=True 且保留已得结果。"""
        self._check_text(text)
        out = []
        pos = 0
        incomplete = False
        try:
            while pos <= len(text):
                m = self._search(text, pos)
                if m is None:
                    break
                out.append(m)
                pos = m.end if m.end > m.start else m.end + 1
        except BudgetExceeded:
            incomplete = True
        return SearchResult(out, incomplete)

    def stream(self):
        return StreamMatcher(self)


class StreamMatcher:
    """分块输入匹配器：feed() 增量喂入，finish() 表示输入结束。

    最长匹配等待：当前尝试仍可能延长（DFA 未死且未到输入末尾）时，
    即使已经过接受状态也不提交匹配，直到后续输入使 DFA 走入死状态
    或 finish() 被调用。已消费的前缀会被丢弃，位置用绝对偏移报告。
    """

    def __init__(self, regex):
        self.regex = regex
        self.det = regex._determinizer()
        self.buf = ""
        self.base = 0      # buf[0] 对应的绝对偏移
        self.scan = 0      # 当前尝试起始（相对 buf）
        self.att = None    # 进行中的尝试 [dfa状态, 位置, 最近接受位置]
        self.finished = False
        self.incomplete = False

    def feed(self, chunk):
        if self.finished:
            raise ValueError("流已结束，不能再喂入")
        if self.incomplete:
            return []
        self.regex._check_text(chunk)
        self.buf += chunk
        return self._pump(False)

    def finish(self):
        self.finished = True
        if self.incomplete:
            return []
        return self._pump(True)

    def _emit(self, s, e):
        caps = self.regex._recover(self.buf, s, e)
        if self.base:
            caps = {k: (None if v is None else (v[0] + self.base,
                                                v[1] + self.base))
                    for k, v in caps.items()}
        return Match(self.base + s, self.base + e, self.buf[s:e], caps)

    def _pump(self, final):
        out = []
        det = self.det
        try:
            while True:
                if self.att is None:
                    if self.scan > len(self.buf):
                        break
                    if self.scan == len(self.buf) and not final:
                        break  # 等待更多输入
                    d = det.start_id
                    last = self.scan if det.is_accept(d) else None
                    self.att = [d, self.scan, last]
                d, p, last = self.att
                while p < len(self.buf) and d is not None:
                    d = det.step(d, self.buf[p])
                    p += 1
                    if d is not None and det.is_accept(d):
                        last = p
                if d is not None and p == len(self.buf) and not final:
                    self.att = [d, p, last]
                    break  # 最长匹配等待：可能还能延长
                self.att = None
                if last is not None:
                    out.append(self._emit(self.scan, last))
                    self.scan = last if last > self.scan else self.scan + 1
                else:
                    self.scan += 1
                if self.att is None and self.scan > 0:
                    # 恢复状态：丢弃已消费前缀，继续扫描
                    dropped = min(self.scan, len(self.buf))
                    self.buf = self.buf[dropped:]
                    self.base += dropped
                    self.scan -= dropped
        except BudgetExceeded:
            self.incomplete = True
        return out
