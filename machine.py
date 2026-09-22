"""发送/接收状态机模型。

字母表（输入/输出动作集合）与转移结构均显式给出，
不依赖任何现成的正则表达式或自动机库。

方差约定（显式）：
- 输入分支逆变：候选机可以接收规范未要求的额外输入，仍然兼容；
- 输出分支协变：候选机若发出规范未允许的额外输出，则不兼容。
"""
from __future__ import annotations

from dataclasses import dataclass


class MachineError(ValueError):
    """机器定义不一致时抛出。"""


@dataclass(frozen=True)
class Transition:
    src: str
    action: str
    dst: str


class Machine:
    """通信协议的发送/接收状态机。

    - inputs:  接收动作（记作 a?），由环境发起，机器必须能够接收；
    - outputs: 发送动作（记作 a!），由机器主动发出。
    """

    def __init__(self, name, states, initial, inputs, outputs, transitions):
        self.name = str(name)
        self.states = frozenset(states)
        self.inputs = frozenset(inputs)
        self.outputs = frozenset(outputs)
        overlap = self.inputs & self.outputs
        if overlap:
            raise MachineError(
                f"{self.name}: 动作同时被声明为输入与输出: {sorted(overlap)}"
            )
        if not self.states:
            raise MachineError(f"{self.name}: 状态集合为空")
        if initial not in self.states:
            raise MachineError(f"{self.name}: 初始状态 {initial!r} 不在状态集合中")
        self.initial = initial

        alphabet = self.inputs | self.outputs
        self._post: dict[tuple[str, str], set[str]] = {}
        self._transitions: list[Transition] = []
        for entry in transitions:
            src, action, dst = entry
            if src not in self.states or dst not in self.states:
                raise MachineError(
                    f"{self.name}: 转移 ({src}, {action}, {dst}) 引用了未声明的状态"
                )
            if action not in alphabet:
                raise MachineError(
                    f"{self.name}: 转移 ({src}, {action}, {dst}) 的动作不在字母表中"
                )
            self._post.setdefault((src, action), set()).add(dst)
            self._transitions.append(Transition(src, action, dst))

    def is_input(self, action):
        return action in self.inputs

    def is_output(self, action):
        return action in self.outputs

    def post(self, state, action):
        """state 经 action 可达的后继状态集合（允许非确定性）。"""
        return frozenset(self._post.get((state, action), ()))

    def enabled(self, state, action):
        return bool(self.post(state, action))

    def enabled_inputs(self, state):
        return frozenset(a for a in self.inputs if self.enabled(state, a))

    def enabled_outputs(self, state):
        return frozenset(a for a in self.outputs if self.enabled(state, a))

    def to_dict(self):
        return {
            "name": self.name,
            "states": sorted(self.states),
            "initial": self.initial,
            "inputs": sorted(self.inputs),
            "outputs": sorted(self.outputs),
            "transitions": [
                [t.src, t.action, t.dst]
                for t in sorted(
                    self._transitions, key=lambda t: (t.src, t.action, t.dst))
            ],
        }

    @classmethod
    def from_dict(cls, data):
        return cls(
            name=data["name"],
            states=data["states"],
            initial=data["initial"],
            inputs=data["inputs"],
            outputs=data["outputs"],
            transitions=data["transitions"],
        )
