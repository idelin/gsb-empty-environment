"""区间 + 因果约束上的必然偏序推导。

模型（小整数域，t_e ∈ Z）：
  * 区间约束：  low_e ≤ t_e ≤ high_e  —— 闭区间，事件是“点”，不确定集是区间。
  * 因果边：    t_v ≥ t_u + min_gap   —— 严格先后，min_gap ≥ 1。
  * 必然先后：  在 *所有* 可行整数赋时中 t_u < t_v。
  * 不可比较：  存在 u≤v 的见证，也存在 v≤u 的见证（含同时刻）。

闭区间相接 [0,2] 与 [2,3] 只保证 ≤（可同时刻 2），不构成先后；
[0,2] 与 [3,4] 保证间隔 ≥1，是必然先后。不使用任何区间中点。

推导用 max-plus（差分约束）Floyd–Warshall；判定用小整数域完全枚举
作独立核验。结果以传递归约（紧凑表示）+ 全闭包两种形式给出。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product

from .constraints import CausalEdge, IntervalConstraint

NEG = -(10**30)


@dataclass(frozen=True)
class Relation:
    """一条必然先后关系及其原始约束证据链。"""

    earlier: str
    later: str
    gap: int
    witness_constraints: tuple[str, ...]
    support: tuple[str, ...]  # 同 witness_constraints，便于显式命名

    @property
    def evidence(self) -> str:
        return " → ".join(self.witness_constraints)


@dataclass(frozen=True)
class Cycle:
    """一条矛盾（正权）环及其原始约束证据。"""

    nodes: tuple[str, ...]
    edges: tuple[str, ...]
    total_gap: int
    due_to_bounds: bool

    @property
    def evidence(self) -> str:
        return " → ".join(self.edges)


@dataclass(frozen=True)
class Witness:
    """一个可行整数赋时（各事件的 tick）。"""

    assignment: dict[str, int]


@dataclass(frozen=True)
class Analysis:
    feasible: bool
    events: tuple[str, ...]
    layers: tuple[tuple[str, ...], ...] = ()
    relations: tuple[Relation, ...] = ()
    closure: tuple[Relation, ...] = ()
    incomparable: tuple[tuple[str, str], ...] = ()
    witness: Witness | None = None
    cycles: tuple[Cycle, ...] = ()
    enumerated_solutions: int = 0
    enumeration_truncated: bool = False
    enum_matches_closure: bool = True


class TemporalOrder:
    """维护原始约束集，撤回/更正后整体重新推导（失去支持的关系即消失）。"""

    def __init__(self, default_domain: tuple[int, int] = (0, 100)) -> None:
        self._intervals: dict[str, IntervalConstraint] = {}
        self._causal: dict[str, CausalEdge] = {}
        self._extra_events: set[str] = set()
        self.default_domain = default_domain
        self._cached: Analysis | None = None

    # ---- 约束录入（输入顺序不影响结果，内部一律按名字排序处理）----

    def add_event(self, name: str) -> None:
        self._extra_events.add(name)
        self._cached = None

    def add_interval(
        self, cid: str, event: str, low: int, high: int
    ) -> None:
        self._intervals[cid] = IntervalConstraint(cid, event, low, high)
        self._cached = None

    def correct_interval(
        self, cid: str, low: int | None = None, high: int | None = None
    ) -> None:
        old = self._intervals[cid]
        self._intervals[cid] = old.corrected(low, high)
        self._cached = None

    def add_causal(
        self, cid: str, earlier: str, later: str, min_gap: int = 1
    ) -> None:
        self._causal[cid] = CausalEdge(cid, earlier, later, min_gap)
        self._cached = None

    def retract(self, cid: str) -> None:
        """撤回一条原始约束；整体重算，失去全部支持的先后关系被删除。"""
        exists = cid in self._intervals or cid in self._causal
        self._intervals.pop(cid, None)
        self._causal.pop(cid, None)
        if not exists:
            raise KeyError(f"不存在约束 {cid!r}")
        self._cached = None

    # ---- 基础结构 ----

    @property
    def constraints(self) -> dict[str, IntervalConstraint | CausalEdge]:
        merged: dict[str, IntervalConstraint | CausalEdge] = {}
        merged.update(self._intervals)
        merged.update(self._causal)
        return merged

    def _events_sorted(self) -> list[str]:
        names: set[str] = set(self._extra_events)
        names.update(c.event for c in self._intervals.values())
        names.update(c.earlier for c in self._causal.values())
        names.update(c.later for c in self._causal.values())
        return sorted(names)

    def _domains(self, events: list[str]) -> dict[str, tuple[int, int]]:
        lo0, hi0 = self.default_domain
        lows = {e: lo0 for e in events}
        highs = {e: hi0 for e in events}
        for c in self._intervals.values():
            lows[c.event] = max(lows[c.event], c.low)
            highs[c.event] = min(highs[c.event], c.high)
        return {e: (lows[e], highs[e]) for e in events}

    def _base_edges(
        self, events: list[str]
    ) -> list[tuple[int, int, int, str]]:
        """(u, v, w, cid)：t_v ≥ t_u + w。"""
        index = {e: i for i, e in enumerate(events)}
        edges: list[tuple[int, int, int, str]] = []
        for c in sorted(self._causal.values(), key=lambda x: x.cid):
            edges.append((index[c.earlier], index[c.later], c.min_gap, c.cid))
        domains = self._domains(events)
        anchor = len(events)
        for e, i in index.items():
            lo, hi = domains[e]
            edges.append((anchor, i, lo, f"low:{e}"))
            edges.append((i, anchor, -hi, f"high:{e}"))
        return edges

    # ---- max-plus Floyd–Warshall（含锚点 0 = t_0，t_0=0）----

    def _floyd(
        self, events: list[str]
    ) -> tuple[list[list[int]], list[list[int | None]], list[tuple[int, int, int, str]], int]:
        n = len(events)
        size = n + 1
        anchor = n
        edges = self._base_edges(events)
        dist = [[NEG] * size for _ in range(size)]
        parent: list[list[int | None]] = [[None] * size for _ in range(size)]
        for i in range(size):
            dist[i][i] = 0
        # 平行边取最大权重；并列时按 (w, cid) 确定取舍，保证可复现。
        chosen: dict[tuple[int, int], tuple[int, str]] = {}
        for u, v, w, cid in edges:
            prev = chosen.get((u, v))
            if prev is None or (w, cid) > prev:
                chosen[(u, v)] = (w, cid)
        for (u, v), (w, _cid) in chosen.items():
            dist[u][v] = w
            parent[u][v] = u
        for k in range(size):
            dk = dist[k]
            for i in range(size):
                dik = dist[i][k]
                if dik == NEG:
                    continue
                di = dist[i]
                for j in range(size):
                    if dk[j] == NEG:
                        continue
                    cand = dik + dk[j]
                    if cand > di[j]:
                        di[j] = cand
                        # 前驱语义：j 在最优 i→j 路上的前驱，来自 k→j 段
                        parent[i][j] = parent[k][j]
        return dist, parent, sorted(chosen.items()), anchor

    @staticmethod
    def _reconstruct(
        parent: list[list[int | None]], src: int, dst: int
    ) -> list[int] | None:
        if parent[src][dst] is None:
            return None
        path = [dst]
        cur = dst
        guard = len(parent) + 1
        while cur != src:
            cur = parent[src][cur]
            if cur is None:
                return None
            path.append(cur)
            guard -= 1
            if guard < 0:
                return None
        path.reverse()
        return path

    def _cycles(
        self,
        events: list[str],
        dist: list[list[int]],
        parent: list[list[int | None]],
        chosen: list[tuple[tuple[int, int], tuple[int, str]]],
        anchor: int,
    ) -> tuple[Cycle, ...]:
        size = len(events) + 1
        labels = events + ["0"]
        weight: dict[tuple[int, int], int] = {
            (u, v): w for (u, v), (w, _cid) in chosen
        }
        cid_of: dict[tuple[int, int], str] = {
            (u, v): cid for (u, v), (_w, cid) in chosen
        }

        def edge_text(u: int, v: int) -> str:
            cid = cid_of[(u, v)]
            cons = self.constraints
            if cid in cons:
                return cons[cid].evidence
            # low:/high: 边界边由对应区间约束作证
            kind, event = cid.split(":", 1)
            for c in self._intervals.values():
                if c.event == event:
                    return f"{cons[c.cid].evidence} （{kind}界）"
            return f"{labels[u]}→{labels[v]} 权 {weight[(u, v)]}（无区间证据）"

        found: list[Cycle] = []
        seen: set[tuple[str, ...]] = set()
        for i in range(size):
            if dist[i][i] <= 0:
                continue
            # 正权对角来自某个 k：i→k 与 k→i 两条严格正权路（k=i 时为自环边）
            seq: list[int] | None = None
            for k in range(size):
                if k == i:
                    if weight.get((i, i), 0) > 0:
                        seq = [i, i]
                        break
                    continue
                if dist[i][k] == NEG or dist[k][i] == NEG:
                    continue
                p1 = self._reconstruct(parent, i, k)
                p2 = self._reconstruct(parent, k, i)
                if p1 is not None and p2 is not None:
                    seq = p1 + p2[1:]
                    break
            if seq is None:
                continue
            edge_ids: list[str] = []
            total = 0
            for a, b in zip(seq, seq[1:]):
                edge_ids.append(edge_text(a, b))
                total += weight[(a, b)]
            node_names = tuple(labels[x] for x in seq[:-1])
            key = tuple(sorted(node_names))
            if key in seen:
                continue
            seen.add(key)
            found.append(
                Cycle(
                    nodes=node_names,
                    edges=tuple(edge_ids),
                    total_gap=total,
                    due_to_bounds=anchor in seq[:-1],
                )
            )
        return tuple(found)

    # ---- 必然关系、紧凑归约、层 ----

    def _support_for(
        self,
        events: list[str],
        parent: list[list[int | None]],
        src: int,
        dst: int,
    ) -> tuple[str, ...]:
        path = self._reconstruct(parent, src, dst)
        if path is None:
            return ()
        cids: list[str] = []
        index = {e: i for i, e in enumerate(events)}
        anchor = len(events)
        for a, b in zip(path, path[1:]):
            if a != anchor and b != anchor:
                for c in self._causal.values():
                    if index[c.earlier] == a and index[c.later] == b:
                        cids.append(c.cid)
                        break
                else:
                    cids.append(f"interval:{events[min(a,b)]}:{events[max(a,b)]}")
            else:
                event_idx = b if a == anchor else a
                for c in self._intervals.values():
                    if index[c.event] == event_idx:
                        cids.append(c.cid)
                        break
        # 去重保序
        seen: set[str] = set()
        uniq = [x for x in cids if not (x in seen or seen.add(x))]
        return tuple(uniq)

    def _closure_and_reduction(
        self,
        events: list[str],
        dist: list[list[int]],
        parent: list[list[int | None]],
        anchor: int,
    ) -> tuple[tuple[Relation, ...], tuple[Relation, ...]]:
        n = len(events)
        closure: list[Relation] = []
        for i in range(n):
            for j in range(n):
                if i != j and dist[i][j] >= 1:
                    support = self._support_for(events, parent, i, j)
                    closure.append(
                        Relation(events[i], events[j], dist[i][j], support, support)
                    )
        closure.sort(key=lambda r: (r.earlier, r.later))
        gap = {(i, j): dist[i][j] for i in range(n) for j in range(n)}
        reduction: list[Relation] = []
        for rel in closure:
            i, j = events.index(rel.earlier), events.index(rel.later)
            redundant = any(
                k != i
                and k != j
                and dist[i][k] >= 1
                and dist[k][j] >= 1
                and dist[i][k] + dist[k][j] >= gap[(i, j)]
                for k in range(n)
            )
            if not redundant:
                reduction.append(rel)
        reduction.sort(key=lambda r: (r.earlier, r.later))
        return tuple(closure), tuple(reduction)

    @staticmethod
    def _layers(
        events: list[str], relations: tuple[Relation, ...]
    ) -> tuple[tuple[str, ...], ...]:
        outgoing: dict[str, set[str]] = {e: set() for e in events}
        indeg: dict[str, int] = {e: 0 for e in events}
        for r in relations:
            if r.later not in outgoing[r.earlier]:
                outgoing[r.earlier].add(r.later)
                indeg[r.later] += 1
        remaining = set(events)
        layers: list[tuple[str, ...]] = []
        while remaining:
            current = sorted(e for e in remaining if indeg[e] == 0)
            if not current:  # 理论上不会发生（无环）
                raise RuntimeError("归约图中出现环")
            layers.append(tuple(current))
            for e in current:
                remaining.discard(e)
                for nxt in outgoing[e]:
                    if nxt in remaining:
                        indeg[nxt] -= 1
        return tuple(layers)

    # ---- 小整数域完全枚举：独立核验 ----

    def enumerate_solutions(
        self, cap: int = 20000
    ) -> tuple[list[dict[str, int]], bool]:
        events = self._events_sorted()
        domains = self._domains(events)
        if any(lo > hi for lo, hi in domains.values()):
            return [], False
        bounds: dict[str, tuple[int, int]] = {
            e: domains[e] for e in events
        }
        causal = list(self._causal.values())
        solutions: list[dict[str, int]] = []
        truncated = False
        ranges = [range(bounds[e][0], bounds[e][1] + 1) for e in events]
        for values in product(*ranges):
            assign = dict(zip(events, values))
            ok = all(
                assign[c.later] - assign[c.earlier] >= c.min_gap for c in causal
            )
            if ok:
                solutions.append(assign)
                if len(solutions) >= cap:
                    truncated = True
                    break
        return solutions, truncated

    def analyze(self, enum_cap: int = 20000) -> Analysis:
        if self._cached is not None:
            return self._cached
        events = self._events_sorted()
        if not events:
            result = Analysis(feasible=True, events=())
            self._cached = result
            return result
        dist, parent, chosen, anchor = self._floyd(events)
        cycles = self._cycles(events, dist, parent, chosen, anchor)
        if cycles:
            result = Analysis(
                feasible=False,
                events=tuple(events),
                cycles=cycles,
            )
            self._cached = result
            return result

        closure, reduction = self._closure_and_reduction(events, dist, parent, anchor)
        layers = self._layers(events, reduction)
        incomparable = tuple(
            (events[i], events[j])
            for i in range(len(events))
            for j in range(i + 1, len(events))
            if dist[i][j] < 1 and dist[j][i] < 1
        )
        witness = Witness({e: dist[anchor][i] for i, e in enumerate(events)})

        solutions, truncated = self.enumerate_solutions(cap=enum_cap)
        if solutions:
            necessary: set[tuple[str, str]] = set()
            for x in range(len(events)):
                for y in range(len(events)):
                    if x == y:
                        continue
                    if all(sol[events[x]] < sol[events[y]] for sol in solutions):
                        necessary.add((events[x], events[y]))
            closure_pairs = {(r.earlier, r.later) for r in closure}
            matches = necessary == closure_pairs
            if not truncated:
                enum_incomp = {
                    tuple(sorted((events[x], events[y])))
                    for x in range(len(events))
                    for y in range(x + 1, len(events))
                    if (events[x], events[y]) not in necessary
                    and (events[y], events[x]) not in necessary
                }
                matches = matches and enum_incomp == set(incomparable)
        else:
            matches = False

        result = Analysis(
            feasible=True,
            events=tuple(events),
            layers=layers,
            relations=reduction,
            closure=closure,
            incomparable=incomparable,
            witness=witness,
            enumerated_solutions=len(solutions),
            enumeration_truncated=truncated,
            enum_matches_closure=matches,
        )
        self._cached = result
        return result
