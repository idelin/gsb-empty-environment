"""运行样例：展示 call-by-need 表达式图的关键行为。

用 ``python3.11 demo.py`` 运行。
"""
from __future__ import annotations

from lazygraph import BlackHoleError, Interpreter, Tracer

SEPARATOR = "=" * 68


def section(title: str) -> None:
    print(f"\n{SEPARATOR}\n{title}\n{SEPARATOR}")


def show(source: str) -> None:
    print("源码:")
    for line in source.strip("\n").splitlines():
        print(f"  | {line}")


def main() -> None:
    # 样例 1：未被使用的除零表达式不会出错 + 调试轨迹
    section("样例 1：未被使用的除零表达式不报错（if 只强制被选中的分支）")
    source = """
let boom = 1 / 0
in if false then boom else 42
"""
    show(source)
    tracer = Tracer()
    result = Interpreter(source, tracer).run()
    print(f"结果: {result}")
    print("调试轨迹（强制求值链）:")
    print(tracer.render_trace())

    # 样例 2：同一 thunk 引用十次只求值一次
    section("样例 2：同一 thunk 被引用十次，函数体只执行一次（tick 计数为 1）")
    source = """
let x = tick()
in x + x + x + x + x + x + x + x + x + x
"""
    show(source)
    tracer = Tracer()
    result = Interpreter(source, tracer).run()
    print(f"结果: {result}    tick() 真正执行次数: {tracer.tick_count}")

    # 样例 3：嵌套强制链轨迹
    section("样例 3：强制求值链展示依赖是如何一层层被拉动的")
    source = """
let a = tick() + 1
let b = a + 10
let c = b + 100
c
"""
    show(source)
    tracer = Tracer()
    result = Interpreter(source, tracer).run()
    print(f"结果: {result}")
    print("调试轨迹:")
    print(tracer.render_trace())

    # 样例 4：异常也缓存（同一个异常对象被重复抛出）
    section("样例 4：异常也是结果，第二次引用命中缓存的同一异常对象")
    source = """
let boom = 1 / 0
boom + boom
"""
    show(source)
    interp = Interpreter(source)
    try:
        interp.run()
    except Exception as error:  # noqa: BLE001 - 示例里展示即可
        first_error = error
        boom = interp.top_thunks["boom"]
        print(f"第一次强制 boom 抛出: {error.message}")
        print(f"boom 状态: {boom.status}")
        try:
            boom.force()
        except Exception as second:  # noqa: BLE001
            print(f"第二次强制 boom 抛出: {second.message}")
            print(f"是否同一个异常对象: {second is first_error}")
    print("调试轨迹:")
    print(interp.tracer.render_trace())

    # 样例 5：直接自引用黑洞
    section("样例 5：直接自引用黑洞")
    source = "let rec a = a in a"
    show(source)
    interp = Interpreter(source)
    try:
        interp.run()
    except BlackHoleError as error:
        print(error.render_chain(interp.sourcemap))

    # 样例 6：间接自引用黑洞（a -> b -> c -> a）
    section("样例 6：间接自引用黑洞，环诊断给出每一环的源码位置")
    source = """
let rec a = b
and b = c
and c = a
in a
"""
    show(source)
    interp = Interpreter(source)
    try:
        interp.run()
    except BlackHoleError as error:
        print(error.render_chain(interp.sourcemap))
    try:
        interp.result_thunk.force()
    except BlackHoleError as error:
        print(f"\n再次强制根表达式直接重抛缓存的黑洞: {error.message}")

    print()


if __name__ == "__main__":
    main()
