# 教学编译器：有限属性求值器（增量、可回放）

纯 TypeScript、零依赖实现的教学用属性求值器：在 AST 上计算**名称绑定**、
**常量值**和**类型**，显式记录继承属性与综合属性之间的依赖，编辑后只重算
受影响的属性。不依赖任何现成编译器框架。

## 语言

块、顺序声明（`const`、`func`）、整数/布尔表达式（字面量、引用、二元/一元
运算）、嵌套函数及其参数声明。作用域规则：

- 声明从其后语句起可见（顺序可见性）；
- 内层遮蔽外层；
- 同块重复声明是错误，定位到后一个声明；
- 函数名在自身体内可见（允许递归），参数与函数体顶层声明同属一个作用域。

## 属性体系（src/semantics.ts）

| 属性 | 类别 | 含义 |
| --- | --- | --- |
| `scopeBase@block` | 继承 | 进入块作用域时的环境（函数体含函数名与参数） |
| `env@node` | 继承 | 语句/表达式处可见的环境 |
| `envOut@stmt` | 综合 | 语句之后的环境（声明加入绑定） |
| `cell@block:name` | 综合 | 块作用域中名字 `name` 的第一个声明节点 |
| `bind@ref` | 综合 | 名称绑定：引用解析到的声明节点 id |
| `type@node` | 综合 | 表达式/声明的类型 |
| `value@node` | 综合 | 常量值（参数/函数为 `unknown`，错误为 `error`） |
| `errors@node` | 综合 | 该节点的诊断 |

**缓存键**一律是 `属性@节点ID`（如 `bind@n10`）或 `cell@块ID:名字`
（如 `cell@n13:b`），绝不只用变量名；节点 ID 在构造时分配，移动语句不改 ID。

## 依赖的显式记录（src/evaluator.ts）

每个属性实例是一个按需计算的槽位。计算中每次读取其他槽位都会记录一条边
进 `depLog`（`observer -> dependency`），可用 `depsOf/rdepsOf/explain/depPath`
查询。真实执行（`node examples/replay.ts`）打印的跨属性依赖链：

```
value@n17 -> value@n6 -> value@n5 -> value@n3 -> value@n2        （常量值链）
bind@n3 -> env@n3 -> env@n6 -> envOut@n2 -> env@n2 -> scopeBase@n26 （继承↔综合交替）
```

**跨属性循环**：常量定义可以引用前面可见的声明，潜在环路是
`value@decl → value@init → bind@ref → env@ref → envOut@decl → value@decl`。
本语言中 `envOut@decl` 只依赖 `cell`/`env`（即声明的*名字*），从不依赖
`value@decl`，加上顺序可见性，这条链无法闭合——依赖图按构造无环。求值器
仍用求值栈做循环检测，一旦违反即抛出带具体路径的错误，例如
`cyclic attribute dependency: a -> b -> a`（见 test/dependencies.test.ts）。

## 增量策略

- **环境哈希-consing**（src/env.ts）：环境是不可变 cons 表且按内容 intern，
  值相等即引用相等，重算后值未变则不提升版本号（equality cutoff）。
- **失效传播**：编辑使种子槽位变脏并沿反向依赖边传播；`type`/`value` 这类
  纯属性间槽位先做**依赖验证**（版本未变则保留缓存，计入 `verified-clean`）。
- **结构读取属性**（`env/envOut/scopeBase/cell/bind/errors`）直接读 AST 结构，
  其依赖集本身可能过期，因此变脏后总是重算（量小且便宜）。
- **编辑种子规则**：只失效"compute 直接读取被改结构"的属性——块语句表
  （`env@` 各语句 + 该块所有 `cell`）、参数改名（`scopeBase@` 函数体 +
  该体 `cell`）、表达式替换（持有者的 `type/value/errors`）；其余由依赖图
  自动传播。删除的子树槽位整体移除。
- **未绑定引用**记录对*每一层*包围块作用域 cell 的依赖（包括当前没有任何
  声明的块），之后插入声明即可修复错误（回放 step 5 演示）。

## 错误定位

诊断形如 `n3.type: unbound reference 'a'`、`n7.envOut: duplicate declaration
of 'x' in block n13`——节点 ID + 属性名。

## 参考实现对照（src/reference.ts）

`referenceCheck` 是从头按作用域遍历的独立实现（显式作用域栈、无缓存）。
测试在每次编辑后把增量求值器的完整快照（每语句环境、每引用绑定、类型、
值、诊断）与参考实现逐一 deep-equal。覆盖场景：移动声明跨过引用、同名
遮蔽、删除外层定义、参数改名、批量 AST 编辑（批量 ≡ 逐条 ≡ 参考）。

## 运行

需要 Node ≥ 22.6（内置 type stripping，直接运行 .ts；开发实测 v23.9.0）。

```sh
node --test test/*.test.ts   # 19 个测试
node examples/replay.ts      # 可回放编辑样例
```

`examples/replay.output.txt` 是真实执行记录，test/replay.test.ts 会重新
执行并逐字节比对。回放共 9 步：初始检查（含依赖图与具体依赖路径）、
移动声明跨过引用并移回、插入遮蔽声明、删除外层定义、插入声明修复未绑定
引用、参数改名与改回、批量编辑。每步打印
`invalidated/recomputed/verified-clean` 计数、诊断及与参考实现的一致性。

## 文件

- `src/ast.ts` — AST 类型、稳定节点 ID、构造器
- `src/env.ts` — 哈希-consing 持久环境
- `src/evaluator.ts` — 通用增量属性求值器（依赖日志、验证、循环检测）
- `src/semantics.ts` — 语言属性定义
- `src/program.ts` — CheckedProgram：编辑、诊断、快照
- `src/reference.ts` — 从头遍历的参考实现
- `examples/replay.ts` + `examples/replay.output.txt` — 可回放样例与真实结果
- `test/` — node:test 测试（语义、增量、依赖、回放）
