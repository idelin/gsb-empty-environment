"""独立虚拟文件系统重放器：判定缓存有效性的客观判据。

任务程序是只读操作序列（mini-DSL）：
  ("open", path, hid)   打开并把句柄记为 hid
  ("read", hid)         按句柄读
  ("lookup", path)      纯查询（可失败）
  ("enum", path, limit) 目录枚举，limit=None 为完整枚举

判据：在变更后的文件系统上重放同一任务程序，若全部观察结果
（成功/失败类别、对象身份、版本、内容、枚举成员）与原始轨迹一致，
则缓存客观有效，否则必须失效。内核的判定不得比该判据更乐观。
"""
from __future__ import annotations

from .vfs import FSError, VFS

MUTATION_DISPATCH = {
    "write": lambda v, a: v.write(a[0], a[1]),
    "create": lambda v, a: v.create(a[0], a[1] if len(a) > 1 else ""),
    "mkdir": lambda v, a: v.mkdir(a[0]),
    "rename": lambda v, a: v.rename(a[0], a[1]),
    "link": lambda v, a: v.link(a[0], a[1]),
    "symlink": lambda v, a: v.symlink(a[0], a[1]),
    "set_target": lambda v, a: v.set_target(a[0], a[1]),
    "unlink": lambda v, a: v.unlink(a[0]),
    "chmod": lambda v, a: v.chmod(a[0], a[1]),
}


def run_task_program(vfs: VFS, program: list[tuple],
                     handles: dict | None = None) -> list[tuple]:
    """执行任务程序，返回可比较的观察向量（同时写入轨迹）。

    观察向量只含任务可观察结果（成功/错误类别、内容、枚举成员），
    不含内部对象 ID 与版本——路径与对象身份本就不是任务的可观察量。
    """
    obs: list[tuple] = []
    if handles is None:
        handles = {}
    for step in program:
        op = step[0]
        if op == "open":
            try:
                h = vfs.open(step[1])
                handles[step[2]] = h
                obs.append(("open", "ok"))
            except FSError as e:
                obs.append(("open", "err", e.kind))
        elif op == "read":
            if step[1] not in handles:      # 前序 open 已失败
                obs.append(("read", "err", "no_handle"))
                continue
            content = vfs.read(handles[step[1]])
            obs.append(("read", "ok", content))
        elif op == "lookup":
            try:
                vfs.lookup(step[1])
                obs.append(("lookup", "ok"))
            except FSError as e:
                obs.append(("lookup", "err", e.kind))
        elif op == "enum":
            try:
                names = vfs.enum(step[1], limit=step[2])
                obs.append(("enum", "ok", tuple(names)))
            except FSError as e:
                obs.append(("enum", "err", e.kind))
        else:
            raise ValueError(f"未知任务操作: {step!r}")
    return obs


def is_legal_mutation(vfs: VFS, mutation: tuple) -> bool:
    try:
        MUTATION_DISPATCH[mutation[0]](vfs, mutation[1:])
        return True
    except (FSError, ValueError):
        return False


def run_mutations(vfs: VFS, mutations: list[tuple]) -> None:
    for m in mutations:
        MUTATION_DISPATCH[m[0]](vfs, m[1:])


def oracle_valid(spec_factory, task_program: list[tuple],
                 mutations: list[tuple],
                 interleave_at: list[int] | None = None) -> bool:
    """客观判据。

    interleave_at: 可选，把第 k 个变更插入到任务程序第 interleave_at[k] 步之前，
    用于模拟读写交错；缺省为全部变更发生在任务完成后。
    """
    # 原始执行（可能交错）
    vfs1 = VFS()
    spec_factory(vfs1)
    obs1 = _run_interleaved(vfs1, task_program, mutations, interleave_at)
    # 终态重放：先应用全部变更，再重跑任务程序
    vfs2 = VFS()
    spec_factory(vfs2)
    run_mutations(vfs2, mutations)
    obs2 = run_task_program(vfs2, task_program)
    return obs1 == obs2


def _run_interleaved(vfs: VFS, program, mutations, interleave_at):
    if not interleave_at:
        obs = run_task_program(vfs, program)
        run_mutations(vfs, mutations)
        return obs
    obs: list[tuple] = []
    handles: dict[str, int] = {}
    mut_by_pos: dict[int, list[tuple]] = {}
    for k, pos in enumerate(interleave_at):
        mut_by_pos.setdefault(pos, []).append(mutations[k])
    for i, step in enumerate(program):
        for m in mut_by_pos.get(i, []):
            MUTATION_DISPATCH[m[0]](vfs, m[1:])
        obs += run_task_program(vfs, [step], handles)
    for m in mut_by_pos.get(len(program), []):
        MUTATION_DISPATCH[m[0]](vfs, m[1:])
    return obs
