"""有限状态模型与进程 ID 置换规范化。

全局状态约定为 ``(shared, locals_)``：

- ``shared``：共享变量部分（任意可哈希值），不参与置换；
- ``locals_``：长度为进程数的元组，``locals_[pid]`` 为进程 ``pid`` 的局部状态。

局部状态必须可比较（如 int / str），以便在对称组内排序得到唯一代表。
对称性由角色（Role）保证：迁移函数只依赖 ``(local, shared)``，与进程 ID 无关，
因此同角色进程天然可交换；``Model`` 在构造时校验声明的对称组不与特殊角色混同。
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from itertools import combinations
from typing import Callable, Hashable, Iterable, Optional, Sequence, Tuple

GlobalState = Tuple[Hashable, Tuple[Hashable, ...]]
TransitionFn = Callable[[Hashable, Hashable], Iterable[Tuple[Hashable, Hashable]]]


class SymmetryError(ValueError):
    """声明的对称组不合法：角色混同、下标越界、组间重叠或组过小。"""


@dataclass(frozen=True)
class Role:
    """一类进程的行为模板。同名角色即同构进程。"""

    name: str
    initial: Hashable
    transitions: TransitionFn  # (local, shared) -> iterable of (new_local, new_shared)


class Model:
    """给定有限状态模型：角色序列 + 初始共享值 + 不变式 + 对称组声明。"""

    def __init__(
        self,
        name: str,
        roles: Sequence[Role],
        initial_shared: Hashable,
        invariant: Callable[[GlobalState], bool],
        groups: Optional[Sequence[Sequence[int]]] = None,
    ) -> None:
        if not roles:
            raise ValueError("模型至少需要一个进程")
        self.name = name
        self.roles = tuple(roles)
        self.initial_shared = initial_shared
        self.invariant = invariant
        if groups is None:
            groups = self._groups_by_role()
        self.groups: Tuple[Tuple[int, ...], ...] = self._validate_groups(groups)

    # ---------------------------------------------------------------- 基本接口

    @property
    def num_processes(self) -> int:
        return len(self.roles)

    def initial_state(self) -> GlobalState:
        return (self.initial_shared, tuple(r.initial for r in self.roles))

    def successors(self, state: GlobalState) -> Iterable[Tuple[int, GlobalState]]:
        """枚举一步迁移，产出 ``(触发进程 pid, 后继状态)``，顺序确定。"""
        shared, locals_ = state
        for pid, role in enumerate(self.roles):
            for new_local, new_shared in role.transitions(locals_[pid], shared):
                updated = list(locals_)
                updated[pid] = new_local
                yield pid, (new_shared, tuple(updated))

    # ----------------------------------------------------- 置换规范化与 ID 映射

    def canonicalize(self, state: GlobalState) -> Tuple[GlobalState, Tuple[int, ...]]:
        """返回 ``(规范代表, mapping)``。

        在每个对称组内按局部状态排序（同值按原 pid 升序，保证确定性），
        组外位置（如领导者）保持不动。``mapping[pos]`` 表示规范代表第 ``pos``
        个位置上的局部状态来自原进程 ``mapping[pos]``，据此可还原原状态。
        """
        shared, locals_ = state
        n = len(locals_)
        new_locals = list(locals_)
        mapping = list(range(n))
        for group in self.groups:
            positions = sorted(group)
            ordered = sorted(positions, key=lambda i: (locals_[i], i))
            for pos, orig in zip(positions, ordered):
                new_locals[pos] = locals_[orig]
                mapping[pos] = orig
        return (shared, tuple(new_locals)), tuple(mapping)

    @staticmethod
    def restore(canonical: GlobalState, mapping: Sequence[int]) -> GlobalState:
        """由规范代表与 ID 映射还原具体状态（``canonicalize`` 的逆）。"""
        shared, canon_locals = canonical
        locals_: list = [None] * len(canon_locals)
        for pos, orig in enumerate(mapping):
            locals_[orig] = canon_locals[pos]
        return shared, tuple(locals_)

    @staticmethod
    def swap(state: GlobalState, i: int, j: int) -> GlobalState:
        """交换两个进程的局部状态（共享部分不变）。"""
        shared, locals_ = state
        updated = list(locals_)
        updated[i], updated[j] = updated[j], updated[i]
        return shared, tuple(updated)

    # ----------------------------------------------------------- 对称条件验证

    def audit_symmetry(self, max_states: int = 100_000) -> Optional[bool]:
        """在具体状态空间上有界枚举，验证声明的对称组确实满足对称条件：

        对每个可达状态 s、组内任意对换 (i, j)，都有
        ``canon(succ(swap(s,i,j))) == canon(swap(succ(s),i,j))``（作为集合）。

        返回 True/False；若状态数超过 ``max_states`` 未能枚举完，返回 None（未决）。
        """
        init = self.initial_state()
        seen = {init}
        queue = deque([init])
        while queue:
            state = queue.popleft()
            succs = [t for _, t in self.successors(state)]
            for group in self.groups:
                for i, j in combinations(group, 2):
                    swapped = self.swap(state, i, j)
                    lhs = {self.canonicalize(self.swap(t, i, j))[0] for t in succs}
                    rhs = {self.canonicalize(t)[0] for _, t in self.successors(swapped)}
                    if lhs != rhs:
                        return False
            for nxt in succs:
                if nxt not in seen:
                    seen.add(nxt)
                    if len(seen) > max_states:
                        return None
                    queue.append(nxt)
        return True

    # --------------------------------------------------------------- 内部工具

    def _groups_by_role(self) -> list:
        by_role: dict = {}
        for pid, role in enumerate(self.roles):
            by_role.setdefault(role.name, []).append(pid)
        return [sorted(pids) for pids in by_role.values() if len(pids) >= 2]

    def _validate_groups(self, groups: Sequence[Sequence[int]]) -> Tuple[Tuple[int, ...], ...]:
        seen: set = set()
        normalized = []
        for group in groups:
            group = tuple(sorted(group))
            if len(group) < 2:
                raise SymmetryError(f"对称组至少含两个进程: {group}")
            role_names = set()
            for pid in group:
                if not 0 <= pid < self.num_processes:
                    raise SymmetryError(f"进程 ID 越界: {pid}")
                if pid in seen:
                    raise SymmetryError(f"进程 {pid} 出现在多个对称组中")
                seen.add(pid)
                role_names.add(self.roles[pid].name)
            if len(role_names) != 1:
                raise SymmetryError(
                    f"对称组 {group} 混同了不同角色的进程: {sorted(role_names)}"
                )
            normalized.append(group)
        return tuple(normalized)
