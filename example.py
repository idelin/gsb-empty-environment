"""运行样例: 双时钟加热器模型。

位置: off -> heating -> ready -> done(目标)
- off     --(reset x)----------------> heating
- heating --(x>=2, reset y)----------> ready
- ready   --(y>=1 且 y<3, 严格小于)--> done
invariant: ready 处 y<=3
"""

from timed_automaton import (
    Constraint,
    TimedAutomaton,
    Transition,
    Verdict,
    check_reachability,
    format_trajectory,
)

TIME_BOUND = 10


def build_model() -> TimedAutomaton:
    return TimedAutomaton(
        clocks=("x", "y"),
        initial="off",
        transitions=(
            Transition("power_on", "off", "heating", resets=frozenset({"x"})),
            Transition(
                "heated",
                "heating",
                "ready",
                guard=(Constraint("x", ">=", 2),),
                resets=frozenset({"y"}),
            ),
            Transition(
                "serve",
                "ready",
                "done",
                guard=(Constraint("y", ">=", 1), Constraint("y", "<", 3)),
            ),
        ),
        invariants={"ready": (Constraint("y", "<=", 3),)},
    )


def main() -> None:
    automaton = build_model()
    print(f"模型最大常数 K = {automaton.max_constant}, 截断阈值 = K+1")
    print(f"全局时间上界 time_bound = {TIME_BOUND}")
    result = check_reachability(automaton, target="done", time_bound=TIME_BOUND)
    print(f"结论: {result.verdict.name}, 已探索状态数: {result.explored_states}")
    if result.verdict is Verdict.REACHABLE:
        print("时间推进轨迹(已经 replay 校验):")
        print(format_trajectory(automaton, result.trace, TIME_BOUND))


if __name__ == "__main__":
    main()
