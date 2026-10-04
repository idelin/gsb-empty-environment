# cellengine — 增量单元格公式引擎

Go 标准库实现的电子表格单元格引擎：公式按需惰性求值，`IF` 只有被
执行的分支才产生真实依赖；批量修改生成新版本并只重算受影响单元格；
旧版本保留只读查询；附带一个每次从头求值的独立参考实现做随机对照。

## 公式语法

公式可带可选的前导 `=`：

```
expr    := term   { ('+' | '-') term }
term    := factor { '*' factor }
factor  := '-' factor | primary
primary := INT | REF | '#ERR!' | 'IF(' expr ',' expr ',' expr ')' | '(' expr ')'
REF     := 一个以上字母 + 一个以上数字（如 A1、BC12，大小写不敏感，统一大写）
```

- `INT`：非负整数（int64）；负数用一元 `-` 表示。
- `IF(c, a, b)`：`c != 0` 时取 `a`，否则取 `b`。**只有条件和被选中的
  分支会被求值**，也只有它们进入依赖图。
- `#ERR!`：错误字面量。错误沿算术与 `IF` 条件传播；未选中分支里的
  错误不传播。

## 值与错误

| 错误 | 含义 |
|---|---|
| `#REF!(ID)` | 引用了不存在（或已删除）的单元格，证据含具体 ID |
| `#CYCLE!(A1->B1->A1)` | 实际求值路径上的环，证据含环上全部单元格 ID |
| `#ERR!` | 错误字面量或其传播 |
| `#PARSE!(...)` | 公式解析失败 |

未执行分支中的环**不是**错误：`A1 = IF(0, A1, 42)` 得 `42`；只有当
分支被选中、求值路径真正成环时才报 `#CYCLE!`。

## 增量语义

- `Apply(ops...)` 把一批 set/del 作为**一个原子版本**生效：同批修改
  彼此可见，读者不会看到中间混合状态。
- 每批结束后按上一版依赖图反向传播得到脏集合，只失效并重算受影响
  单元格；求值时按最新公式重新记录实际读取的依赖边（缺失引用也记
  边，所以之后补上该单元格会触发重算），并返回本次**真正重算**的
  单元格 ID 列表。
- 缓存按版本有效性维护：未受影响的单元格直接沿用缓存；受影响的一
  律重算，不会跨版本误用旧值。
- 每个版本保存不可变快照，`GetAt(version, id)` 支持历史只读查询。

## 参考实现与差分测试

`Reference(cells)` 是独立的非增量求值器：对给定公式集合从零开始、
无跨版本状态地算出每个单元格，同样只求值 `IF` 被选分支。它与引擎
只共享解析器和 `Value` 类型，求值逻辑独立编写。
`diff_test.go` 用多个随机种子生成短操作序列（随机 set/del 组成的小
批次），每批之后对照：

1. 当前版本全部单元格与参考实现一致；
2. 随机抽一个历史版本与其参考快照一致；
3. 未被报告为重算的单元格值保持不变（缓存未跨版本误用）。

## 命令行

```
go run ./cmd/sheet        # 从 stdin 逐行读命令
```

```
set <CELL> <formula>   设置公式（'=' 可省），单独成为一个版本
del <CELL>             删除单元格
begin ... commit       把多条 set/del 合并为一个版本
get <CELL> [@version]  查询当前或历史版本的值
cells                  列出当前版本全部单元格
versions               列出每个版本实际重算的单元格
quit                   退出
```

示例：

```
set B1 1
set D1 A1 + 1
set A1 IF(B1, C1, D1)   # B1=1 时 D1 分支不执行，潜藏环不报错
set C1 10
get A1                  # A1 = 10
set B1 0                # 切到 D1 分支，真正成环
get A1                  # A1 = #CYCLE!(A1->D1->A1)
set B1 1                # 环解除
get A1                  # A1 = 10
```

## 测试

```
go test ./...
```

结果（Go 1.24，darwin/arm64）：

```
=== RUN   TestRandomSequencesAgainstReference   # 5 个种子 × 400 批随机操作对照参考实现
--- PASS: TestRandomSequencesAgainstReference (0.47s)
=== RUN   TestReferenceIsLazyToo                # 参考实现同样只求被选分支
--- PASS: TestReferenceIsLazyToo (0.00s)
=== RUN   TestBasicArithmeticAndPrecedence      # 常量/引用/+-*/、优先级、一元负号
--- PASS: TestBasicArithmeticAndPrecedence (0.00s)
=== RUN   TestConditionalCycleIntroduceAndRemove # 条件切换引入/解除环，依赖边随之切换
--- PASS: TestConditionalCycleIntroduceAndRemove (0.00s)
=== RUN   TestUnexecutedBranchSelfCycleIsNotError # 未执行分支中的自环不是错误
--- PASS: TestUnexecutedBranchSelfCycleIsNotError (0.00s)
=== RUN   TestErrorPropagationAlongDeps         # 错误沿依赖传播、修复后清除
--- PASS: TestErrorPropagationAlongDeps (0.00s)
=== RUN   TestMissingRefDeleteAndReAdd          # 缺失引用报错，补上/删除触发重算
--- PASS: TestMissingRefDeleteAndReAdd (0.00s)
=== RUN   TestDiamondSharedSubgraph             # 菱形共享子图各重算一次
--- PASS: TestDiamondSharedSubgraph (0.00s)
=== RUN   TestBatchSwapDependencies             # 批量交换依赖、同批可见
--- PASS: TestBatchSwapDependencies (0.00s)
=== RUN   TestOldVersionQueriesAreStable        # 旧版本只读查询不被后续修改影响
--- PASS: TestOldVersionQueriesAreStable (0.00s)
=== RUN   TestCacheNotReusedAcrossVersions      # 缓存不跨版本误用
--- PASS: TestCacheNotReusedAcrossVersions (0.00s)
=== RUN   TestParseErrors                       # 解析错误与其传播
--- PASS: TestParseErrors (0.00s)
=== RUN   TestCycleEvidenceNamesConcreteCells   # 环证据包含具体单元格 ID
--- PASS: TestCycleEvidenceNamesConcreteCells (0.00s)
PASS
ok      cellengine
```

## 文件结构

- `value.go` — `Value` 类型与各类错误构造（环证据规范化旋转，保证确定性）
- `parse.go` — 词法/递归下降解析器（引擎与参考实现共享）
- `engine.go` — 增量引擎：批量版本、依赖图维护、脏集合重算、历史快照
- `reference.go` — 独立从头求值的参考实现
- `engine_test.go` — 上述场景化单元测试
- `diff_test.go` — 随机短操作序列与参考实现的差分对照
- `cmd/sheet/main.go` — 命令行前端
