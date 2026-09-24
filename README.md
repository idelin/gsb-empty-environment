# 符号 DAG 偏求值器（部分绑定 · 精确化简 · 规则证书）

纯 Python 3.11 标准库实现（`fractions.Fraction` 精确有理数），不调用任何
符号代数库，不执行宿主 `eval`。表达式以哈希共享（hash-consed）DAG 表示，
支持部分变量绑定下的精确求值与化简，未绑定节点保留为符号。

## 文件

| 文件 | 作用 |
|---|---|
| `dag.py` | 哈希共享节点工厂、规范化键、全域性（totality）等定义域分析 |
| `interp.py` | 独立原式解释器（参考语义，错误带源位置） |
| `engine.py` | 增量偏求值引擎：化简规则、规则证书、依赖图、撤销与 GC |
| `parser.py` | 手写递归下降解析器（`+ - * /`、负号、`cond(c,t,e)`、有理数） |
| `demo.py` | 运行样例 |
| `tests/test_simplify.py` | unittest 套件（含随机对照测试） |

## 语义与定义域

- 值域为精确有理数；`cond(c,t,e)`：c 非零取 t，否则取 e。
- 唯一定义域限制：除数不得为 0。未定义用 `⊥@pos` 表示，`pos` 是源表达式
  中造成未定义的节点位置（如除以零的除法节点）。
- 解释器严格按从左到右、深度优先求值，报告**第一个**错误的位置。
- 化简保持原表达式定义域：`0 * x → 0`、`x - x → 0` 等规则仅在 `x` 可证
  全域（total）时适用；`0 * (1/y)` 不会被化为 0，`0 * (1/0)` 化简为
  `⊥` 而不是 0。
- `⊥` 传播尊重求值顺序：仅当左侧操作数全域时，右侧的 `⊥` 才可提前传播，
  保证“第一个错误位置”与原式解释器一致。
- 交换律规范化（`+`/`*` 操作数排序）仅在至少一侧全域时进行，避免改变
  错误报告顺序。

## 规则证书

每一步变换产出一条证书：规则名、变换前后、**适用域条件**及该条件成立的
理由，例如：

```
#003 [sub-self] (- y y)  ~~>  0   (requires: D(a); holds: x - x = 0 requires D(x); holds: variable (defined everywhere))
#001 [mul-zero-left] (* 0 (+ y 1))  ~~>  0   (requires: D(b); holds: 0 · x = 0 requires D(b); holds: all subterms total)
```

规则未适用时（如 `0 * (1/y)`）写入 journal 说明保留原因。

## 增量更新

- `Engine.set(name, value)` 绑定/更新/解绑（`None`）变量；沿依赖图标记
  脏节点，**撤销过期的常量结论**（journal `retract` 事件），只重算受影响
  子图。
- `cond` 条件变为常量时折叠为所选分支，**移除对未使用分支的依赖**并回收
  其缓存状态（journal `gc` 事件）；条件再变时按需重建。
- 结果是 (源 DAG, 绑定) 的纯函数：**绑定顺序不影响最终规范结果**；同名
  变量共享同一绑定。

## 运行样例（`python3 demo.py` 节选）

```
1. Partial evaluation: bound vars fold, unbound nodes stay symbolic
expr           : (+ (+ (+ (* (+ x 0) 1) (- y y)) 1/3) 1/6)
no bindings    : (+ 1/6 (+ x 1/3))
after x = 7/2  : 4

2. Domain preservation: 0 * undefined is NOT 0
expr           : (* 0 (/ 1 y))
simplified     : (* 0 (/ 1 y))   <- kept, y may make 1/y undefined
  journal [note] kept (* 0 (/ 1 y)): right operand not provably total; 0 · undefined ≠ 0
after y = 3    : 0
after y = 0    : ⊥@3   <- undefined, positioned at the div
interpreter on original with y=0: ('undef', 3)
interpreter on simplified with y=0: ('undef', 3)

3. Incremental update: stale constant conclusions are retracted
x = 2          : 6
x = 5          : 12
  journal [retract] node 0: retract constant conclusion 2
  journal [retract] node 2: retract constant conclusion 3
  journal [retract] node 4: retract constant conclusion 6

4. Conditional branch switch: unused dependencies are removed (GC)
x = 1          : (+ a 1)
  journal [gc] node 6: dependency no longer used, state released
  journal [gc] node 4: dependency no longer used, state released
  journal [gc] node 5: dependency no longer used, state released
x = 0          : (* b 2)

5. Binding order does not affect the canonical result
order ('x', 'y', 'z', 'w'): 24
order ('w', 'z', 'y', 'x'): 24
order ('y', 'x', 'w', 'z'): 24
canonical keys identical: True

6. Error positions agree with the independent interpreter
expr           : (+ (/ 1 (- x 2)) (/ 1 (- y y)))
simplified     : ⊥@4
original       : ('undef', 4)
simplified     : ('undef', 4)
```

## 测试结果

`python3 -m unittest discover -s tests`：

```
Ran 26 tests in 0.108s
OK
```

覆盖：精确有理数、部分绑定保留未绑定节点、重复变量同绑定、`0*x`/`x-x`
定义域保护、错误位置一致性（含最左错误、非全域左操作数场景）、增量撤销
常量结论、条件分支切换的依赖 GC、绑定顺序无关性、证书域信息完整性，以及
各 400 例随机全绑定/部分绑定对照独立解释器（值、未定义性、错误位置三者
一致）。另以 3000 例随机种子压力验证零失配。
