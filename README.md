# cellengine — 单元格公式引擎

Go 标准库实现的增量式电子表格单元格引擎。核心特性：**动态依赖** ——
`IF(flag, a, b)` 只有在 `flag` 选定某一分支后，该分支中的引用才成为真实依赖；
条件切换会引入或解除依赖环。

## 公式语法

```
expr    := add
add     := mul (('+' | '-') mul)*
mul     := unary ('*' unary)*
unary   := '-' unary | primary
primary := INT | IDENT | '#ERR!' | '#REF!' | '#CYCLE!'
         | 'IF' '(' expr ',' expr ',' expr ')' | '(' expr ')'
IDENT   := [A-Za-z_][A-Za-z0-9_]*   （单元格 ID，大小写敏感，保留字 IF 除外）
```

语义：

- 整数常量、单元格引用、`+ - *`（含一元负号）、`IF(cond, then, else)`（cond 非零选 then）。
- `IF` 只求值被选中的分支；**只有真正被读取的引用才构成依赖**。
- 错误值：`#REF!`（引用缺失单元格）、`#CYCLE!`（依赖环，附具体单元格 ID 证据）、
  `#ERR!`（显式错误字面量）。错误沿被读取的依赖严格传播；未执行分支中的
  错误与环不影响结果。
- 算术两个操作数都求值，左操作数的错误优先传播。

## 引擎行为

- `Apply(sets, dels)` 把一批修改**原子地**作为一个新版本提交：同批次的修改
  互相可见；公式解析失败则整批拒绝、版本不变。
- 每次提交只重算受影响单元格（变更单元格 + 按当前反向依赖闭包），返回
  真正重算的单元格 ID 列表。
- 环的判定基于最终依赖图的强连通分量：位于真实执行依赖环上的单元格得
  `#CYCLE!` 并携带环证据（如 `X -> Y -> X`）；未执行分支里的环不算错误。
- 引用缺失单元格产生 `#REF!`，但依赖边仍然记录：之后补上（或删除）该单元格
  会精确触发依赖方的重新求值。
- 旧版本只读可查：`Get(id, version)` 按版本二分每格历史；缓存只服务当前版本，
  不跨版本误用。

## 目录结构

- `engine/expr.go` — 词法/语法分析（递归下降）
- `engine/eval.go` — 表达式求值（IF 惰性分支，错误传播）
- `engine/engine.go` — 增量引擎：脏集闭包、按需重算、环锁定 +
  SCC 校正（Tarjan）、逐格版本历史
- `engine/ref.go` — 独立参考实现：每次查询从头求值，用于差分对照
- `cmd/cellengine/main.go` — 命令行前端

## 命令行

```
go run ./cmd/cellengine [脚本文件]   # 无参数时读 stdin
```

命令：`set A = 1+2`、`del A`、`begin`/`end`（组成原子批次）、
`get A`、`get A@3`（历史版本）、`version`、`dump`、`help`、`quit`。
每次提交打印新版本号与真正重算的单元格。

## 测试

```
go test ./...
```

针对性测试（`engine/engine_test.go`）：条件切换引入/解除环、未执行分支中的环
不算错、错误沿依赖传播、删除再补引用、菱形共享子图（每格只重算一次）、
批量交换依赖的原子可见性、版本化只读查询、重算集合最小性、解析失败整批回滚。

差分测试（`engine/diff_test.go`）：300 个随机种子 × 25 步短操作序列
（随机 set/del/批量，公式含 IF、算术、错误字面量、缺失引用），每步后与
从头求值的参考实现逐格对照当前值与随机历史版本；参考同样只求值被选分支。
环证据只比对错误码（多环相交时证据集合可合法不同），并校验证据中的单元格
本身均为 `#CYCLE!`。

测试结果：

```
--- PASS: TestRandomSequencesAgainstReference (4.29s)
--- PASS: TestConditionalSwitchIntroducesAndRemovesCycle
--- PASS: TestUnexecutedBranchCycleIsNotAnError
--- PASS: TestErrorPropagation
--- PASS: TestDeleteAndReaddReference
--- PASS: TestDiamondSharedSubgraph
--- PASS: TestBatchAtomicVisibilityAndSwap
--- PASS: TestVersionedQueries
--- PASS: TestRecomputedMinimality
--- PASS: TestBatchParseErrorIsAtomic
ok      cellengine/engine
```
