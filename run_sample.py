"""运行样例: 完成性判定 + 反例重放 + 重复请求 ID 独立性演示。"""

from checker import analyze, replay
from model import RequestSystem
from samples import (
    build_blocked_protocol,
    build_bounded_protocol,
    build_retry_protocol,
)


def show(model, result):
    print(f"[{result.model}] 初态 {result.initial}")
    print(f"  判定:     {result.verdict.value}")
    print(f"  搜索完整: {result.explored_fully} (可达状态 {len(result.reachable_states)} 个)")
    print(f"  可达终态: {sorted(result.terminals_reached) or '无'}")
    print(f"  阻塞状态: {result.blocking_states or '无'}")
    trace = result.counterexample
    if trace is not None:
        print("  反例轨迹:")
        for i, step in enumerate(trace.steps):
            marker = "   <== 环起点(无限重复)" if trace.loop_start == i else ""
            print(f"    {i:2d}. {step.source} --{step.label}--> {step.target}{marker}")
        rep = replay(model, trace)
        print(f"  重放校验: ok={rep.ok} 末态={rep.final_state} "
              f"终态={rep.is_terminal} 闭环={rep.loop_closed} 环公平={rep.loop_fair}")


def main():
    print("== 1. 无限重试协议: 可能完成但非所有公平路径完成 ==")
    model = build_retry_protocol()
    show(model, analyze(model))

    print("\n== 2. 有界重试协议: 所有公平路径完成 ==")
    model = build_bounded_protocol()
    show(model, analyze(model))

    print("\n== 3. 依赖阻塞协议: 阻塞 ==")
    model = build_blocked_protocol()
    show(model, analyze(model))

    print("\n== 4. 搜索截断(max_states=3): 未决 ==")
    model = build_retry_protocol()
    show(model, analyze(model, max_states=3))

    print("\n== 5. 重复请求 ID 状态独立 ==")
    system = RequestSystem(build_retry_protocol())
    key1 = system.accept("REQ-7")
    key2 = system.accept("REQ-7")
    system.fire(key1, "validate")
    print(f"  实例 {key1}: {system.state_of(key1)}  (已触发 validate)")
    print(f"  实例 {key2}: {system.state_of(key2)}  (未受影响)")


if __name__ == "__main__":
    main()
