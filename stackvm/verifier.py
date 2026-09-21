"""字节码静态验证器。

检查两类问题:
1. 非法控制转移: 跳转目标必须是指令边界 (跳到多字节指令中间、
   越界或落入不可解码区域均被拒绝);
2. 栈高度不一致: 对控制流图做抽象解释, 每条指令入口在所有
   到达它的路径上必须有唯一的栈高度, 同时禁止栈下溢。
"""
from __future__ import annotations

from . import isa


class VerifyError(Exception):
    pass


def verify(code: bytes) -> dict[int, int]:
    """返回 {指令pc: 入口处栈高度}; 验证失败抛 VerifyError。"""
    try:
        boundaries = isa.decode_all(code)
    except isa.DecodeError as exc:
        raise VerifyError(f"字节码解码失败: {exc}") from None

    if not boundaries:
        raise VerifyError("字节码为空")

    # 1) 跳转目标合法性
    for pc, (info, target, _nxt) in boundaries.items():
        if info.name in ("JMP", "JZ"):
            assert target is not None
            if target not in boundaries:
                raise VerifyError(
                    f"pc={pc}: {info.name} 的目标 {target} "
                    f"不是有效指令边界 (越界或落在指令中间)"
                )

    # 2) 栈高度数据流分析
    heights: dict[int, int] = {0: 0}
    worklist = [0]
    while worklist:
        pc = worklist.pop()
        if pc not in boundaries:
            raise VerifyError(
                f"pc={pc}: 控制流到达了非指令边界 (跳到指令中间或流出末尾)"
            )
        info, target, nxt = boundaries[pc]
        height = heights[pc]
        pops, pushes = isa.STACK_EFFECT[info.name]
        if height < pops:
            raise VerifyError(
                f"pc={pc}: 栈下溢, 执行 {info.name} 需要 {pops} "
                f"个值, 栈上只有 {height} 个"
            )
        after = height - pops + pushes

        if info.name == "HALT":
            if after != 0:
                raise VerifyError(
                    f"pc={pc}: HALT 时栈高度为 {after}, 应为 0"
                )
            successors: list[int] = []
        elif info.name == "JMP":
            successors = [target]
        elif info.name == "JZ":
            successors = [target, nxt]
        else:
            successors = [nxt]

        for successor in successors:
            if successor in heights:
                if heights[successor] != after:
                    raise VerifyError(
                        f"pc={successor}: 控制流路径栈高度不一致, "
                        f"已记录 {heights[successor]}, 新路径为 {after}"
                    )
            else:
                heights[successor] = after
                worklist.append(successor)

    return heights
