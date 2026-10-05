# 有限属性求值器（教学编译器）

在 AST 上计算**名称绑定、常量值、类型**的有限属性求值器。纯 TypeScript，
不依赖任何现成编译器框架；Node ≥ 22.6 可直接执行（类型擦除），无需构建步骤。

## 语言

块（block）、顺序声明（`const` / 嵌套 `func` 带参数声明）、整数/布尔表达式、
变量引用。作用域规则：

- 声明从其**后**的语句起可见（顺序可见性）；
- 内层块/函数遮蔽外层同名声明；
- 同一块内重复声明报错（报在 `envOut` 属性上）。

## 属性定义（src/lang.ts）

| 属性      | 类别 | 定义于       | 含义 |
|-----------|------|-------------|------|
| `envIn`   | 继承 | 语句/表达式 | 进入节点时可见的环境 |
| `envOut`  | 综合 | 语句        | 语句之后可见的环境（const/func 扩展；块不外泄） |
| `binding` | 综合 | 引用        | 名字解析到的绑定（含未绑定时的负依赖记录） |
| `type`    | 综合 | 表达式      | `int` / `bool` / `func` / `error` |
| `const`   | 综合 | 表达式      | 编译期常量值（`UNKNOWN` 表示非常量） |
| `errors`  | 综合 | 所有节点    | 诊断列表，自底向上聚合 |
| `blockEnv`| 综合 | 块          | 块内全部声明一次可见（仅教学对照开关，可成环） |

错误位置 = **节点 ID + 属性名**，例如 `n16.binding: unbound variable 'y'`。

## 依赖记录与增量重算（src/evaluator.ts）

- 缓存键 = `(节点ID, 属性名)`，**不用变量名**：同名遮蔽、声明移动都不撞键；
  节点 ID 创建时分配、永不改变（移动语句不改 ID）。
- 每次计算记录精确依赖边（被读属性 → 读者），并记录读取瞬间被读属性的**版本号**；
  未绑定引用同样记录对作用域的依赖：`binding --(via "lookup:y")--> envIn`，
  之后插入声明使 envIn 版本变化，引用自动重算、错误修复（负依赖）。
- 编辑（src/edits.ts）只把**根因属性**标为强制重算（被编辑块的语句
  `envIn/envOut`、块 `errors`），其余沿依赖边传播脏标记；
  读取时按需 verify：逐条比较依赖版本，一致则复用旧值 —— 只重算真正受影响的属性
  （见测试“块内编辑不重算兄弟块的任何属性”）。

## 跨属性依赖路径

常量链 `const a = 1; const b = a + 2; const c = b * 3;` 中，`c` 的常量值
沿以下**继承/综合交替**的路径依赖（test/constants.test.ts 逐边断言）：

```
prodC.const → refB.const → refB.binding → refB.envIn → c.envIn → b.envOut → sumB.const
(综合)       (综合)        (综合)          (继承)        (继承)      (综合)      (综合)
```

**跨属性循环**：顺序可见性下依赖严格按“前序语句 → 后序语句”单向流动，语法层面
无环。打开教学对照开关 `visibility: 'block'`（块内所有声明一次可见）后，
`const a = b; const b = a;` 产生真实循环，求值器抛出 `CycleError` 并给出具体路径
（test/cycle.test.ts 断言，节点 ID 为实际分配值）：

```
n2.envIn → n5.blockEnv → n1.type → n1.binding → n1.envIn → n2.envIn
(继承)      (综合)          (综合)     (综合)        (继承)      回到起点
```

## 参考求值器与对拍

`src/reference.ts` 每次从头按作用域遍历整棵树、不用任何缓存，与增量求值器共享
同一套纯函数。所有编辑测试在每个编辑步骤后做全树快照对拍
（`snapshotIncremental` vs `evaluateReference`，逐 key 比较序列化值）。

## 运行

```sh
node --test test/*.test.ts   # 19 个 Node 测试
node examples/replay.ts      # 可重放编辑样例
```

（`npm test` / `npm run replay` 亦可；需要 Node ≥ 22.6。）

## 真实执行结果

### `node --test test/*.test.ts`（Node v24.19.0）

```
✔ 声明从其后语句起可见；之前引用未绑定
✔ 内层遮蔽外层，外层在块后仍然有效
✔ 同块重复声明在 envOut 属性上报错，并指出首个声明节点
✔ 嵌套函数：参数进入函数体作用域，外层声明对函数体可见，参数不是常量
✔ 缓存键是 (节点ID, 属性名) 而非变量名：同名引用各有独立缓存项
✔ 未绑定引用也记录对作用域的依赖（via=lookup:<名>）
✔ 常量链：const 可引用前面可见的声明并折叠；跨属性依赖链被显式记录
✔ 布尔/比较/逻辑运算的类型与常量折叠
✔ 类型错误报告在出错节点的 type 属性上；非常量操作数使 const 为 UNKNOWN
✔ 引用未绑定名字时 const 为 UNKNOWN、type 为 error
✔ 跨属性循环：block 可见性下 const 互相引用，报告具体依赖路径
✔ 顺序可见性下同样的程序不成环，而是未绑定错误
✔ 移动声明跨过引用：错误随位置出现/消失，节点 ID 不变
✔ 删除外层定义：内层引用变未绑定；插入同名声明后修复（负依赖）
✔ 删除内层遮蔽声明后，引用解析到外层绑定
✔ 参数改名：旧名引用变未绑定，新名引用生效
✔ 批量 AST 编辑：一次应用多处变更，结果与参考版一致
✔ 增量性：块内编辑不重算兄弟块的任何属性
✔ 增量性：块尾追加声明只重算环境骨架，既有表达式属性被 verify 剪枝
ℹ tests 19  ℹ pass 19  ℹ fail 0
```

### `node examples/replay.ts`

初始程序：`const x = 1; const y = x + 2; func f(p:int){ const z = p*y; z } ; y*10`

```
== STEP 0 初始全量求值
   errors:          (none)
   tail (y*10):     const=30 type=int
   stats:           recomputed=61 verified=0 cacheHits=25
   reference check: OK
== STEP 1 move: 把 'const y' 移到 tail 之后（声明跨过引用）
   errors:          n8.binding: unbound variable 'y'; n16.binding: unbound variable 'y'
   tail (y*10):     const=? type=error
   stats:           recomputed=40 verified=8 cacheHits=53
   reference check: OK
== STEP 2 move: 把 'const y' 移回 index 1（错误修复）
   errors:          (none)
   tail (y*10):     const=30 type=int
   reference check: OK
== STEP 3 remove: 删除外层 'const x'（y 的右值变未绑定）
   errors:          n3.binding: unbound variable 'x'
   tail (y*10):     const=? type=error
   reference check: OK
== STEP 4 insert: 在 index 0 插入 'const x = 10'（插入声明修复错误）
   errors:          (none)
   tail (y*10):     const=120 type=int
   reference check: OK
== STEP 5 renameParam: 参数 p -> q（函数体内 p 变未绑定）
   errors:          n7.binding: unbound variable 'p'
   tail (y*10):     const=120 type=int
   stats:           recomputed=21 verified=4 cacheHits=34
   reference check: OK
== STEP 6 batch: [参数改回 p, 'const x' 移到 y 之后, 追加 exprStmt(x)]
   errors:          n3.binding: unbound variable 'x'
   tail (y*10):     const=? type=error
   reference check: OK
全部步骤均与参考求值器一致。
```

每一步的 `recomputed` 远小于全量（61），直观展示“只重算受影响属性”；
`dep of ref y` 打印显示未绑定/已绑定引用对作用域的显式依赖边
（`n16.envIn`, `via: "lookup:y"`，版本号随编辑递增）。

## 文件结构

```
src/ast.ts        AST、稳定节点 ID、父指针索引、结构编辑原语
src/evaluator.ts  通用属性求值器：缓存/依赖图/版本化 verify/循环检测
src/lang.ts       语言属性规则（envIn/envOut/binding/type/const/errors）+ 序列化快照
src/reference.ts  参考求值器：从头按作用域遍历，不缓存
src/edits.ts      insert/remove/move/renameParam/batch 编辑 + 可重放日志
test/             19 个 node:test 测试（含每步与参考版对拍）
examples/replay.ts 可重放编辑样例
```
