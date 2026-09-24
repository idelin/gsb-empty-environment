"""运行样例：交错协程、值传递、finally 清理、终态、重入禁止。

用法：python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from minicoro import Interpreter, ReentrancyError

DEMO = """
# 1) 两个协程交错运行，局部变量互不污染
fn counter(label, n) {
    let i = 0
    while i < n {
        yield label + "#" + str(i)
        i = i + 1
    }
    return label + " done"
}

let a = counter("A", 3)
let b = counter("B", 2)
print("a:", resume(a))
print("b:", resume(b))
print("a:", resume(a))
print("b:", resume(b))
print("a:", resume(a))
print("b 已结束, 再 resume:", resume(b))   # 明确终态
print("state a:", state(a), " state b:", state(b))

# 2) 关闭挂起协程，finally 恰好执行一次
fn guarded() {
    try {
        yield "g1"
        yield "g2"
    } finally {
        print("guarded: finally 清理执行")
    }
}
let g = guarded()
print("g:", resume(g))
close(g)
close(g)                                    # 幂等，不会再次清理
print("state g:", state(g), " resume closed:", resume(g))

# 3) resume 向协程内送值（支持 UTF-8）
fn echo() {
    let got = yield "ready"
    return "收到:" + got
}
let e = echo()
print("e:", resume(e))
print("e:", resume(e, "你好"))
"""


def main():
    it = Interpreter()
    it.run(DEMO)

    # 4) Python 侧 API：Step 终态与重入禁止
    it.run("fn selfy() { resume(me)  yield 1 }")
    co = it.invoke(it.get("selfy"), [])
    it.set_global("me", co)
    try:
        co.resume()
    except ReentrancyError as exc:
        print("运行中重入被禁止:", exc)

    z = it.invoke(it.get("counter"), ["Z", 1])
    print("Python 侧 resume:", z.resume())
    print("Python 侧 resume:", z.resume())
    print("终态后再 resume:", z.resume())


if __name__ == "__main__":
    main()
