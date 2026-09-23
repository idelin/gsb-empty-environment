# symdag — 符号 DAG 部分求值与域保持化简

纯 Python 3.11+（仅标准库：`fractions`、`dataclasses`、`unittest`），不调用任何符号代数库，
不在任何地方使用宿主 `eval`/`exec`（有测试守护）。所有算术都是 `fractions.Fraction` 上的精确有理数计算。

## 功能对照

| 需求 | 实现 |
| --- | --- |
| 部分绑定后精确求值与化简，保留未绑定节点 | `Session.simplify` / `simplify_once`：未绑定变量以 `var` 节点保留在结果 DAG 中 |
| 错误域定位 | 每个节点携带 `origin`（原式中的路径）；`UndefinedError.path` 与 `undef` 节点都报告原式位置 |
| 不执行宿主 eval | 独立解释器 `symdag.interp.interpret` 直接走 DAG；测试扫描源码确保无 `eval(`/`exec(` |
| 绑定更新后增量撤销旧常量结论 | `Session.set/unset` 按 `reads/frees` 依赖精确失效；`stats["retracted_constants"]` 计数 |
| 条件分支改变移除未再使用的依赖 | `if` 条件为常量时折叠到所取分支，父条目的依赖只保留条件与活分支（死分支变量变更不再触发根重算） |
| 保留原表达式定义域 | 化简只在域保持时应用；`0 * x → 0`、`x - x → 0` 等要求 `x` 可证 total，否则记入 `session.suppressed` |
| 0 乘未定义式不能化为 0 | `0 * (1/(x-x))` 化简为 `undefined@...` 而非 `0`；`0 * (1/y)` 保持原样（y 可能为 0） |
| 每次化简产出规则证书 | 每步重写产生 `Certificate(rule, before, after, domain)`，`domain` 说明该步适用域 |
| 对照独立原式解释器 | 差分测试：随机小表达式 × 有限有理输入网格，值与错误路径都必须一致 |
| 有理数精确计算 | 全部使用 `fractions.Fraction`，无浮点、无舍入 |
| 绑定顺序不影响规范结果 | 哈希-consing + 交换律操作数规范序 + 只依赖当前绑定的失效语义；测试三种顺序规范键相同 |
| 重复变量用同绑定 | 同名变量 intern 为同一节点，绑定按名查一次，所有出现共享 |

## 语义与定义域保持

表达式语言：`const q`（有理数）、`var x`、`add/sub/mul/div/neg/if/lt/eq`。
`if(c, a, b)` 在 `c ≠ 0` 时取 `a`。`div` 在除数为 0 时**未定义**；
未定义向上传播（左到右求值，报告第一个失败位置）。

化简保证：对任何变量赋值，原式与化简式**同时有定义且相等，或同时在同一原式位置未定义**。
因此诸如 `0 * x → 0` 仅当 `x` 可证 total（处处有定义）时才应用；
`x` 的 totality 是保守判定（`a/b` 仅当 `b` 为非零常量时 total），且与绑定无关，
因此可以安全地用于增量缓存的重写前提。

## 增量模型

`Session` 为每个节点缓存一个 `Entry(result, reads, frees)`：

- `reads`：得到该结论时读取过的已绑定变量；
- `frees`：结果中仍自由的变量。

`set(name, v)` / `unset(name)` 恰好失效 `name ∈ reads ∪ frees` 的条目
（常量结论随之撤销），其余子树缓存保留并被复用（`stats["hits"]`）。
`if` 折叠到某一分支时，父条目只继承条件与活分支的依赖——
死分支变量的后续变更不会使根结论失效（测试断言根条目对象不变、零重算）。
因为失效语义只依赖当前绑定，最终规范结果与绑定顺序无关。

## 目录结构

```
symdag/
  nodes.py      # 哈希-consed DAG 节点、Factory、totality/free-vars 分析、规范打印
  interp.py     # 独立参考解释器（真值语义），UndefinedError 携带原式路径
  simplify.py   # Session（增量部分求值）、Certificate、全部重写规则及域说明
  gen.py        # 随机小表达式生成器（测试与 demo 共用）
tests/test_symdag.py   # 17 个 unittest：精确性、域保持、增量、差分、顺序无关等
examples/demo.py       # 六段运行样例
```

## 运行

```bash
python3 -m unittest discover -s tests -t . -v   # 测试（Python 3.11+）
python3 examples/demo.py                        # 运行样例
```

## 运行样例输出（节选）

```
2. Domain preservation: 0 * undefined is NOT 0
  expr      : (* 0 (/ 1 (- x x)))
  simplified: undefined@1(division by zero)   (undefined, never 0)
  expr      : (* 0 (/ 1 y))
  simplified: (* 0 (/ 1 y))   (kept: y might be 0)
    [suppressed mul-zero] (* 0 (/ 1 y))

4. Incremental updates: stale constants retracted, live deps kept
  x=1, y=0            -> (* 2 (* 2 z))
  y=3 (branch switch) -> 10
  stats: {'computed': 19, 'hits': 10, 'invalidated': 10, 'retracted_constants': 6}

6. Differential check vs. the independent reference interpreter
  3000 evaluations compared, 184 undefined cases,
  values AND error positions all match. mismatches: 0
```

## 测试结果

```
Ran 17 tests in 0.05s
OK
```

覆盖：精确有理折叠、未绑定节点保留、重复变量单绑定、证书含适用域、
`0*undefined≠0`（两种形态）、`x-x` 的 totality 守卫、错误路径定位、
条件常量折叠、绑定更新撤销常量、无关子树缓存复用、分支切换依赖移除、
绑定顺序无关、无宿主 eval、随机差分（值与错误路径同时比对）。
