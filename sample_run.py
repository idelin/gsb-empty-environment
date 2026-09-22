"""运行样例：带循环的请求/响应协议 vs 三个候选实现。

用法: python3 sample_run.py
"""
import subprocess
import sys
from pathlib import Path

from machine import Machine
from simulation import check_simulation

# 规范：带循环的请求/响应协议
#   idle --req?--> busy --ack!--> ready --data!--> idle （循环）
#   busy --cancel?--> idle
SPEC = Machine(
    name="spec-server",
    states={"idle", "busy", "ready"},
    initial="idle",
    inputs={"req", "cancel"},
    outputs={"ack", "data"},
    transitions=[
        ("idle", "req", "busy"),
        ("busy", "ack", "ready"),
        ("busy", "cancel", "idle"),
        ("ready", "data", "idle"),
    ],
)

# 候选一：多接收一个输入 reset?（输入逆变，允许），少发一个输出 data!（输出协变，允许）
CANDIDATE_OK = Machine(
    name="candidate-ok",
    states={"idle", "busy", "ready"},
    initial="idle",
    inputs={"req", "cancel", "reset"},
    outputs={"ack"},
    transitions=[
        ("idle", "req", "busy"),
        ("idle", "reset", "idle"),
        ("busy", "ack", "ready"),
        ("busy", "cancel", "idle"),
    ],
)

# 候选二：多发出一个规范未允许的输出 error!（输出协变被破坏，不兼容）
CANDIDATE_BAD_OUTPUT = Machine(
    name="candidate-bad-output",
    states={"idle", "busy", "ready"},
    initial="idle",
    inputs={"req", "cancel"},
    outputs={"ack", "data", "error"},
    transitions=[
        ("idle", "req", "busy"),
        ("busy", "ack", "ready"),
        ("busy", "cancel", "idle"),
        ("busy", "error", "idle"),
        ("ready", "data", "idle"),
    ],
)

# 候选三：缺少规范要求的输入 req?（输入逆变被破坏，不兼容）
CANDIDATE_BAD_INPUT = Machine(
    name="candidate-bad-input",
    states={"idle"},
    initial="idle",
    inputs={"cancel"},
    outputs=set(),
    transitions=[],
)


def report(candidate):
    result = check_simulation(candidate, SPEC)
    print(f"== {candidate.name} vs {SPEC.name} ==")
    print(f"不动点迭代 {result.iterations} 轮后终止，"
          f"最大模拟关系含 {len(result.relation)} 个状态对")
    if result.ok:
        print("结论: 兼容（候选机被规范模拟）\n")
        return
    print("结论: 不兼容")
    cex = result.counterexample
    print(cex.format())
    print("逐步重放:")
    cex.replay(candidate, SPEC, on_step=lambda i, st: print(
        f"  重放第 {i} 步: ({st.candidate_src}, {st.spec_src})"
        f" --{st.label()}--> ({st.candidate_dst}, {st.spec_dst}) 通过"))
    print("  终点失败条件复核通过\n")
    return result


def main():
    report(CANDIDATE_OK)
    bad_output = report(CANDIDATE_BAD_OUTPUT)
    report(CANDIDATE_BAD_INPUT)

    # 导出独立可执行的反例脚本并直接运行，验证其可独立执行
    script_path = Path(__file__).with_name("counterexample_replay.py")
    script_path.write_text(
        bad_output.counterexample.to_script(CANDIDATE_BAD_OUTPUT, SPEC),
        encoding="utf-8",
    )
    print(f"== 独立反例脚本已导出: {script_path.name} ==")
    proc = subprocess.run(
        [sys.executable, str(script_path)],
        capture_output=True, text=True,
    )
    print(proc.stdout, end="")
    print(f"独立脚本退出码: {proc.returncode}")
    return 0 if proc.returncode == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
