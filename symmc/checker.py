"""显式状态模型检查：对称压缩 BFS、三值结论、可重放反例。

结论为三值：

- ``Verdict.SAFE``：状态空间搜索完毕，未发现违反不变式的状态；
- ``Verdict.VIOLATED``：发现违反不变式的状态，并给出具体反例路径；
- ``Verdict.PENDING``：因状态数 / 深度上限截断，搜索未完成，结论未决。

开启对称压缩时，每个规范代表都同时保存一个具体代表状态；反例路径完全由
具体状态构成，且每一步附带 ``(规范代表, ID 映射)``，可还原、可逐步重放。
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from typing import List, Optional, Tuple

from .model import GlobalState, Model


class Verdict(Enum):
    SAFE = "safe"
    VIOLATED = "violated"
    PENDING = "pending"


@dataclass
class Step:
    """反例路径中的一步。

    ``pid`` 为触发本步迁移的具体进程 ID（初始状态为 None）；
    ``state`` 为具体状态；``canonical`` 与 ``mapping`` 为其规范代表与
    ID 映射，满足 ``Model.restore(canonical, mapping) == state``。
    """

    pid: Optional[int]
    state: GlobalState
    canonical: GlobalState
    mapping: Tuple[int, ...]


@dataclass
class Result:
    verdict: Verdict
    explored: int  # 已存储的（规范）状态数
    counterexample: Optional[List[Step]] = None

    def replay(self, model: Model) -> bool:
        """在原始（未压缩）模型上逐步重放反例，验证其真实可达且违反性质。"""
        path = self.counterexample
        if not path:
            return False
        if path[0].state != model.initial_state():
            return False
        for step in path:
            canonical, mapping = model.canonicalize(step.state)
            if canonical != step.canonical or mapping != step.mapping:
                return False
            if model.restore(step.canonical, step.mapping) != step.state:
                return False
        for cur, nxt in zip(path, path[1:]):
            if (nxt.pid, nxt.state) not in list(model.successors(cur.state)):
                return False
        return not model.invariant(path[-1].state)


class Checker:
    """对给定有限状态模型做不变式检查。"""

    def __init__(
        self,
        model: Model,
        use_symmetry: bool = True,
        max_states: Optional[int] = None,
        max_depth: Optional[int] = None,
    ) -> None:
        self.model = model
        self.use_symmetry = use_symmetry
        self.max_states = max_states
        self.max_depth = max_depth

    def _normalize(self, state: GlobalState) -> Tuple[GlobalState, Tuple[int, ...]]:
        if self.use_symmetry:
            return self.model.canonicalize(state)
        return state, tuple(range(self.model.num_processes))

    def run(self) -> Result:
        model = self.model
        init = model.initial_state()
        canon0, map0 = self._normalize(init)

        if not model.invariant(init):
            step = Step(pid=None, state=init, canonical=canon0, mapping=map0)
            return Result(Verdict.VIOLATED, explored=1, counterexample=[step])

        reps = {canon0: init}              # 规范代表 -> 一个具体代表状态
        parents: dict = {canon0: None}     # 规范代表 -> (父规范代表, 触发 pid)
        queue = deque([(canon0, 0)])
        truncated = False

        while queue:
            canon, depth = queue.popleft()
            if self.max_depth is not None and depth >= self.max_depth:
                truncated = True
                continue
            rep = reps[canon]
            for pid, nxt in model.successors(rep):
                canon_n, map_n = self._normalize(nxt)
                if not model.invariant(nxt):
                    path = self._build_path(reps, parents, canon)
                    path.append(Step(pid=pid, state=nxt, canonical=canon_n, mapping=map_n))
                    return Result(Verdict.VIOLATED, explored=len(reps), counterexample=path)
                if canon_n in reps:
                    continue
                if self.max_states is not None and len(reps) >= self.max_states:
                    truncated = True
                    continue
                reps[canon_n] = nxt
                parents[canon_n] = (canon, pid)
                queue.append((canon_n, depth + 1))

        verdict = Verdict.PENDING if truncated else Verdict.SAFE
        return Result(verdict, explored=len(reps))

    def _build_path(self, reps: dict, parents: dict, canon: GlobalState) -> List[Step]:
        chain = []
        node = canon
        while node is not None:
            entry = parents[node]
            pid = None if entry is None else entry[1]
            chain.append((pid, node))
            node = None if entry is None else entry[0]
        chain.reverse()
        steps = []
        for pid, canon_node in chain:
            concrete = reps[canon_node]
            real_canon, mapping = self._normalize(concrete)
            steps.append(Step(pid=pid, state=concrete, canonical=real_canon, mapping=mapping))
        return steps
