"""带生效区间与优先级的差分约束系统。

核心性质：
- 顶点、边均为字符串 ID；保留孤立点与重边；区间左闭右开。
- 时间轴被所有区间端点切分为原子分段，每个分段内活跃约束集恒定。
- 求解状态按 (分段, 活跃约束版本集, 相关撤销集) 缓存：
  区间更正只影响被覆盖的分段，未变分段直接命中缓存，
  不会为每个时间点独立复制图。
- push/pop 保存并还原域（约束版本）、证据（决策日志）与优先选择（撤销集）。
"""
from __future__ import annotations

from .model import Constraint, Decision, QueryPiece, SegmentResult
from .solver import bellman_ford


class ConstraintSystem:
    def __init__(self):
        self.vertices: set[str] = set()
        self.constraints: dict[str, Constraint] = {}
        self.revoked: set[str] = set()          # 被软撤销的约束 ID（优先选择）
        self.decisions: list[Decision] = []     # 决策日志（证据），显式输出
        self._history: list[tuple] = []
        self._cache: dict[tuple, SegmentResult] = {}
        self._vversion = 0                      # 顶点集版本，用于缓存键
        self.cache_hits = 0
        self.cache_misses = 0

    # ---------------- 域变更 ----------------

    def add_vertex(self, vid: str):
        if vid not in self.vertices:
            self.vertices.add(vid)
            self._vversion += 1

    def add_constraint(self, cid, u, v, c, start, end, priority):
        """新增或更正约束。同 ID 再次添加视为更正，版本号递增。"""
        self.add_vertex(u)
        self.add_vertex(v)
        old = self.constraints.get(cid)
        version = 1 if old is None else old.version + 1
        self.constraints[cid] = Constraint(cid, u, v, c, start, end, priority, version)

    def retract(self, cid: str):
        del self.constraints[cid]
        self.revoked.discard(cid)

    def batch_correct(self, ops):
        """批量更正。ops 元素：
        ("upsert", cid, u, v, c, start, end, priority)
        ("retract", cid)
        ("add_vertex", vid)
        先整体校验格式，再顺序应用。
        """
        for op in ops:
            if op[0] not in ("upsert", "retract", "add_vertex"):
                raise ValueError(f"未知批量更正操作: {op[0]!r}")
        for op in ops:
            kind = op[0]
            if kind == "upsert":
                self.add_constraint(*op[1:])
            elif kind == "retract":
                self.retract(op[1])
            else:
                self.add_vertex(op[1])

    # ---------------- 探索：push / pop ----------------

    def push(self):
        """快照当前域、证据与优先选择。"""
        self._history.append((
            dict(self.constraints),
            set(self.vertices),
            set(self.revoked),
            len(self.decisions),
        ))

    def pop(self):
        """还原最近一次 push 的域、证据与优先选择。"""
        constraints, vertices, revoked, decisions_len = self._history.pop()
        self.constraints = constraints
        if vertices != self.vertices:
            self.vertices = vertices
            self._vversion += 1
        self.revoked = revoked
        del self.decisions[decisions_len:]

    # ---------------- 分段与求解 ----------------

    def atomic_segments(self, t0, t1):
        """把 [t0, t1) 按所有约束区间端点切成原子分段。"""
        if not t0 < t1:
            raise ValueError(f"时间窗为空: [{t0}, {t1})")
        pts = {t0, t1}
        for con in self.constraints.values():
            s = max(con.start, t0)
            e = min(con.end, t1)
            if s < e:
                pts.add(s)
                pts.add(e)
        pts = sorted(pts)
        return [(pts[i], pts[i + 1]) for i in range(len(pts) - 1)]

    def active_constraints(self, s, e):
        """分段 [s, e) 上活跃（半开区间重叠）的约束，按 cid 排序保证确定性。"""
        return sorted(
            (c for c in self.constraints.values() if c.start < e and c.end > s),
            key=lambda c: c.cid,
        )

    def segment_result(self, s, e) -> SegmentResult:
        """求解单个原子分段；未变化的分段共享缓存结果，不重新建图。"""
        active = self.active_constraints(s, e)
        active_ids = {c.cid for c in active}
        key = (
            s, e,
            tuple((c.cid, c.version) for c in active),
            tuple(sorted(r for r in self.revoked if r in active_ids)),
            self._vversion,
        )
        hit = self._cache.get(key)
        if hit is not None:
            self.cache_hits += 1
            return hit
        self.cache_misses += 1
        edges = [(c.cid, c.u, c.v, c.c) for c in active if c.cid not in self.revoked]
        feasible, potentials, cycle = bellman_ford(self.vertices, edges)
        result = SegmentResult(
            feasible,
            potentials if feasible else None,
            None if feasible else tuple(cycle),
        )
        self._cache[key] = result
        return result

    # ---------------- 查询 ----------------

    def query(self, t0, t1):
        """查询时间窗 [t0, t1) 的最大可行分段及势值。

        返回 QueryPiece 列表：相邻且势值相同的可行分段合并为最大分段；
        冲突分段给出准确的半开区间与原始约束 ID 构成的负环。
        """
        pieces: list[QueryPiece] = []
        for s, e in self.atomic_segments(t0, t1):
            r = self.segment_result(s, e)
            piece = QueryPiece(
                s, e, r.feasible,
                r.potentials if r.feasible else None,
                None if r.feasible else r.cycle,
            )
            if (pieces and piece.feasible and pieces[-1].feasible
                    and pieces[-1].potentials == piece.potentials):
                prev = pieces[-1]
                pieces[-1] = QueryPiece(prev.start, e, True, prev.potentials, None)
            else:
                pieces.append(piece)
        return pieces

    # ---------------- 软撤销 ----------------

    def resolve(self, t0, t1):
        """对 [t0, t1) 内每个冲突分段，反复软撤销负环上优先级最低
        （数值最小，平局取 cid 字典序小者）的约束直至可行。

        每个决策都显式记录进 self.decisions 并返回，绝不静默忽略冲突。
        """
        new_decisions = []
        for s, e in self.atomic_segments(t0, t1):
            while True:
                r = self.segment_result(s, e)
                if r.feasible:
                    break
                victim = min(
                    r.cycle,
                    key=lambda cid: (self.constraints[cid].priority, cid),
                )
                self.revoked.add(victim)
                d = Decision("revoke", victim, (s, e), tuple(r.cycle))
                self.decisions.append(d)
                new_decisions.append(d)
        return new_decisions
