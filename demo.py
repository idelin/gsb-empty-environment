"""运行样例:卫生宏、参数引用、展开限制(展开链)、双重源码映射。"""
from macro_lang import expand_source, render, run_source
from macro_lang.errors import ExpansionLimitError


def banner(title):
    print("=" * 60)
    print(title)
    print("=" * 60)


banner("1. 卫生宏:调用方的 tmp 不被宏内 tmp 覆盖")
SWAP = """\
; 交换两个变量的宏(非 ASCII 注释,验证 UTF-8 字节区间)
(defmacro swap! (a b)
  (let ((tmp a))
    (do (set a b) (set b tmp))))
(let ((x 1) (y 2) (tmp 99))
  (swap! x y)
  (print x y tmp))
"""
values, _ = run_source(SWAP)
print("-> 输出 2 1 99 表示 x/y 已交换,调用方 tmp 保持 99")

banner("2. 引用参数不被无关重命名")
PLUS_ONE = """\
(defmacro plus-one (e) (let ((tmp 1000)) (+ e 1)))
(let ((tmp 41)) (plus-one tmp))
"""
values, _ = run_source(PLUS_ONE)
print(f"-> (plus-one tmp) = {values[-1]}(实参 tmp 仍指调用方的 41,而非宏内的 1000)")

banner("3. 展开结果(可见卫生重命名 tmp#N)")
expanded, _ = expand_source("(defmacro m (x) (let ((tmp x)) (+ tmp tmp)))\n(m 7)")
print(render(expanded[0]))

banner("4. 递归宏达到深度上限:报告完整展开链")
BOOM = "(defmacro boom (x) (boom (boom x)))\n(boom 1)"
try:
    expand_source(BOOM, max_depth=6)
except ExpansionLimitError as err:
    print(err)

banner("5. 节点数上限:指数膨胀被拦截")
DUP = "(defmacro dup (x) (+ x x))\n(dup (dup (dup (dup (dup (dup (dup 1)))))))"
values, _ = run_source(DUP)
print(f"默认限制下 (dup^7 1) = {values[-1]}")
try:
    expand_source(DUP, max_nodes=50)
except ExpansionLimitError as err:
    print(err)

banner("6. 双重源码映射:定义处 + 调用处(UTF-8 字节区间)")
_, expander = expand_source(SWAP)
data = SWAP.encode("utf-8")


def show(span):
    return data[span.start : span.end].decode("utf-8")


print(f"{'节点':<10} {'宏':<8} {'定义处字节区间':<16} {'调用处字节区间':<16} 定义处源码")
for entry in expander.source_map[:8]:
    print(
        f"{entry.node_text:<10} {entry.macro_name:<8} "
        f"{entry.def_span.start}-{entry.def_span.end:<12} "
        f"{entry.call_span.start}-{entry.call_span.end:<12} "
        f"{show(entry.def_span)!r}"
    )
