"""差分解释器：在有限测试域上分别解释执行两个程序并比对可观察行为。

注意：这只能在给定有限测试域上给出“未发现差异”的证据，
不能也不应声称全域（所有可能输入）语义等价的证明。
"""
from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from interp import run
from ir import Instr

DEFAULT_DOMAIN = tuple(range(-2, 3))  # 每个输入变量取 [-2, 2] 的整数


@dataclass
class DiffResult:
    total: int
    mismatches: list[tuple[tuple, object, object]]
    n_inputs: int
    domain: tuple

    @property
    def equivalent_on_domain(self) -> bool:
        return not self.mismatches

    def report(self) -> str:
        dom = f"{self.n_inputs} 个输入变量 × 每变量 {len(self.domain)} 个取值"
        if self.equivalent_on_domain:
            return (f"有限测试域等价：{self.total} 个测试用例（{dom}）的可观察行为全部一致。"
                    f"注意：这并非全域等价证明，结论仅覆盖上述有限测试域。")
        lines = [f"发现 {len(self.mismatches)}/{self.total} 个测试用例行为不一致（{dom}）："]
        for case, ra, rb in self.mismatches[:5]:
            lines.append(f"  输入 {case}: 原程序 {ra}  vs  变换后 {rb}")
        return "\n".join(lines)


def num_inputs(prog: list[Instr]) -> int:
    ks = [int(ins.args[1]) for ins in prog if ins.op == "input"]
    return max(ks) + 1 if ks else 0


def _snapshot(prog: list[Instr], inputs: tuple):
    """可观察行为快照：正常输出序列，或异常/超时状态。"""
    try:
        out, _ = run(prog, inputs)
        return ("ok", tuple(out))
    except Exception as exc:  # 运行时错误、步数超限都纳入比对
        return ("error", f"{type(exc).__name__}: {exc}")


def differential(prog_a: list[Instr], prog_b: list[Instr],
                 domain: tuple = DEFAULT_DOMAIN) -> DiffResult:
    n = max(num_inputs(prog_a), num_inputs(prog_b))
    cases = list(product(domain, repeat=n))
    mismatches = []
    for case in cases:
        ra, rb = _snapshot(prog_a, case), _snapshot(prog_b, case)
        if ra != rb:
            mismatches.append((case, ra, rb))
    return DiffResult(len(cases), mismatches, n, domain)
