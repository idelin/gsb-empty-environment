"""测试公共工具：小对象集规格、任务程序执行、判据对比。"""
from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from trace_kernel.kernel import AnalysisKernel
from trace_kernel.replayer import run_mutations, run_task_program
from trace_kernel.trace import TraceLog
from trace_kernel.vfs import VFS


def small_spec() -> VFS:
    """小对象集：
    /a: f(file 'f1'), l(硬链接->f), s(符号链接->/a/f)
    /b: g('g1'), h('f1' 与 f 内容相同)
    /c: 不可读目录, 内含 secret
    """
    vfs = VFS()
    vfs.populate({
        "a": {"f": "f1", "s": ("->", "/a/f")},
        "b": {"g": "g1", "h": "f1"},
        "c": {"secret": "s1"},
    })
    vfs.link("/a/f", "/a/l")
    vfs.chmod("/c", False)
    vfs.trace.records.clear()   # 初始结构不计入轨迹
    return vfs


# 任务程序（只读操作序列）
PROGRAMS = {
    "P-read-file":      [("open", "/a/f", "h0"), ("read", "h0")],
    "P-via-symlink":    [("open", "/a/s", "h0"), ("read", "h0")],
    "P-failed-lookup":  [("lookup", "/a/missing")],
    "P-enum-full":      [("enum", "/a", None)],
    "P-enum-prefix":    [("enum", "/a", 1)],
    "P-via-hardlink":   [("open", "/a/l", "h0"), ("read", "h0")],
    "P-other-subtree":  [("open", "/b/g", "h0"), ("read", "h0")],
    "P-perm-denied":    [("open", "/c/secret", "h0")],
    "P-read-twice":     [("open", "/a/f", "h0"), ("read", "h0"), ("read", "h0")],
}

# 合法变更操作集（在 small_spec 上）
MUTATIONS = {
    "M-write-f":       [("write", "/a/f", "f2")],
    "M-rename-f":      [("rename", "/a/f", "/a/f2")],
    "M-unlink-l":      [("unlink", "/a/l")],
    "M-unlink-f":      [("unlink", "/a/f")],
    "M-create-missing": [("create", "/a/missing", "m1")],
    "M-retarget-same": [("set_target", "/a/s", "/b/h")],   # 最终内容相同
    "M-create-new":    [("create", "/a/new", "n1")],
    "M-chmod-a":       [("chmod", "/a", False)],
    "M-write-g":       [("write", "/b/g", "g2")],
    "M-chmod-c":       [("chmod", "/c", True)],
    "M-link-g2":       [("link", "/b/g", "/a/g2")],
    "M-replace-f":     [("unlink", "/a/f"), ("create", "/a/f", "f3")],
}


def run_case(program, mutations, task="T"):
    """原始交错执行（任务完成后应用变更），返回 (kernel, obs_original)。"""
    vfs = small_spec()
    vfs.current_task = task
    obs = run_task_program(vfs, program)
    vfs.current_task = None
    run_mutations(vfs, mutations)
    kernel = AnalysisKernel()
    kernel.ingest(vfs.trace.records)
    kernel.evaluate()
    return kernel, obs


def oracle_obs(program, mutations):
    """判据：在变更后的终态文件系统上重放任务程序。"""
    vfs = small_spec()
    run_mutations(vfs, mutations)
    return run_task_program(vfs, program)
