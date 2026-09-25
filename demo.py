"""运行样例：尾位置调用识别、帧复用、逻辑调用轨迹与 finally 清理顺序。

用法：python3.11 demo.py
"""

import sys

from tailcall import parse, run


def show(title):
    print()
    print("=" * 64)
    print(title)
    print("=" * 64)


def main():
    show("1. 尾递归十万次：物理帧不增长（Python 递归上限临时调到 100）")
    src = (
        "\n"
        "def sum_to(n, acc) = if n == 0 then acc else sum_to(n - 1, acc + n)\n"
        "sum_to(100000, 0)\n"
    )
    program = parse(src)
    old_limit = sys.getrecursionlimit()
    sys.setrecursionlimit(100)
    try:
        m = run(program)
    finally:
        sys.setrecursionlimit(old_limit)
    print("结果             :", m.result)
    print("物理帧峰值       :", m.max_frames)
    print("尾调用帧复用次数 :", m.tail_reuse_count)

    show("2. 可读逻辑调用轨迹：fact(3, 1)（↻ 表示尾调用复用帧）")
    src = (
        "\n"
        "def fact(n, acc) = if n == 0 then acc else fact(n - 1, acc * n)\n"
        "0 + fact(3, 1)\n"
    )
    m = run(parse(src), trace=True)
    for line in m.trace_log:
        print(line)
    print("结果:", m.result)

    show("3. 1 + f() 不是尾调用：待完成的加法阻止帧复用")
    src = (
        "\n"
        "def f(n) = if n == 0 then 0 else 1 + f(n - 1)\n"
        "f(5)\n"
    )
    m = run(parse(src), trace=True)
    for line in m.trace_log:
        print(line)
    print("结果:", m.result, "，物理帧峰值:", m.max_frames, "（随递归深度增长）")

    show("4. try/finally：清理顺序必须保持（正常与异常两种情形）")
    src = (
        'def leaf() = emit("leaf: 业务完成")\n'
        'def guarded() = try leaf() finally emit("cleanup: guarded 清理")\n'
        'try guarded() finally emit("cleanup: 外层清理")\n'
    )
    m = run(parse(src))
    print("正常执行输出顺序:")
    for item in m.output:
        print(" ", item)
    src = (
        'def boom() = 1 / 0\n'
        'def inner() = try boom() finally emit("cleanup: inner 清理")\n'
        'try inner() finally emit("cleanup: outer 清理")\n'
    )
    m = run(parse(src))
    print("异常展开输出顺序:")
    for item in m.output:
        print(" ", item)
    print("错误:", m.error.message)

    show("5. 错误报告：UTF-8 字节区间 + 逻辑调用轨迹")
    src = ("# 注释：中文字符占多个字节\n"
           "def boom(n) = if n == 0 then 1 / 0 else boom(n - 1)\n"
           "boom(10)")
    m = run(parse(src))
    print(m.error)
    span = m.error.span
    data = src.encode("utf-8")
    print("字节区间 {}..{} 对应的源码: {!r}".format(span[0], span[1],
                                                  data[span[0]:span[1]]))


if __name__ == "__main__":
    main()
