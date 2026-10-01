"""有限请求-响应协议的有限状态模型。

规则:
- 终态显式声明(如 COMPLETED / CANCELLED),取消也是合法终态;
- 公平性仅作用于声明的公平组(fair_groups),客户端动作不假设公平;
- 每个被接受的请求是独立实例,重复请求 ID 状态互不影响。
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Transition:
    """一条迁移: source --label--> target,归属公平组 group。"""

    source: str
    label: str
    target: str
    group: str = "env"


class ProtocolModel:
    """显式有限状态迁移系统。"""

    def __init__(self, name, states, terminals, transitions, initial, fair_groups=()):
        self.name = name
        self.states = frozenset(states)
        self.terminals = frozenset(terminals)
        self.transitions = tuple(transitions)
        self.initial = initial
        self.fair_groups = frozenset(fair_groups)

        if initial not in self.states:
            raise ValueError(f"初态 {initial!r} 不在状态集中")
        if not self.terminals <= self.states:
            raise ValueError("终态必须是状态集的子集")
        for t in self.transitions:
            if t.source not in self.states or t.target not in self.states:
                raise ValueError(f"迁移端点未声明: {t!r}")

        self._by_source: dict[str, list[Transition]] = {s: [] for s in self.states}
        for t in self.transitions:
            self._by_source[t.source].append(t)

    def is_terminal(self, state):
        return state in self.terminals

    def transitions_from(self, state):
        return list(self._by_source.get(state, []))


class RequestSystem:
    """请求实例管理器。

    规则: 每次 accept 都创建独立实例,键为 (request_id, 序号);
    相同 request_id 重复接受时,各实例状态完全独立。
    """

    def __init__(self, model):
        self.model = model
        self._next_seq: dict[str, int] = {}
        self._states: dict[tuple[str, int], str] = {}

    def accept(self, request_id):
        seq = self._next_seq.get(request_id, 0)
        self._next_seq[request_id] = seq + 1
        key = (request_id, seq)
        self._states[key] = self.model.initial
        return key

    def state_of(self, key):
        return self._states[key]

    def fire(self, key, label):
        current = self._states[key]
        for t in self.model.transitions_from(current):
            if t.label == label:
                self._states[key] = t.target
                return t
        raise ValueError(f"实例 {key} 在状态 {current!r} 无法触发 {label!r}")
