# skewguard — 写倾斜检测与声明式约束断言器

仅用 Go 标准库实现的内存事务库：把跨多行的业务不变量（如「医生值班表至少 1 人值班」）
声明为显式谓词，事务在**提交阶段**基于「最新已提交状态 + 本事务写集」**重验谓词**，
从而检测快照隔离下不可见的并发交叉修改（写倾斜 / write skew），并安全回滚破坏不变量的事务。

## 设计

- `DB`：已提交状态 + 全局约束表；提交在互斥锁下串行化，提交时重验等价于可串行化判定。
- `Tx`：开启时拍摄一致性快照；读写均走「快照 + 自身写集」视图（read-your-writes）。
- `Tx.Assert(name, pred)`：事务级声明式断言，立即在事务视图上求值（快速失败），
  并在提交时基于最新已提交状态重验——这是写倾斜检测的核心。
- `DB.RegisterConstraint(name, pred)`：全局不变量，每次提交都会重验。
- 任一谓词失败 → 返回 `*ConstraintError`（含约束名），事务整体回滚，已提交状态零污染。

谓词通过只读 `View`（`Get` / 前缀 `Scan`）求值，天然表达多行约束。

## 验收场景

A、B 同时值班；A 看到 B 在而申请请假，B 同时看到 A 在而申请请假。
两事务在提交阶段重验「至少 1 人值班」：先提交者使值班人数 2→1 通过；
后提交者重验时发现人数将 1→0，谓词失败，被精准中止并回滚。最终值班人数恒为 1。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
=== 场景 1：顺序请假（无并发，第二人被不变量拦截）===
A 请假成功，当前值班人数: 1
B 请假被中止: skewguard: commit aborted: assertion "at-least-one-on-duty" violated
最终值班人数: 1

=== 场景 2：并发请假（写倾斜，提交阶段精准中止一人）===
B 的事务读取快照：看到 A 状态 = on（认为可以请假）
A 的事务读取快照：看到 B 状态 = on（认为可以请假）
A 请假事务：skewguard: commit aborted: assertion "at-least-one-on-duty" violated（已安全回滚）
B 请假事务：提交成功
最终值班人数: 1 （不变量守住：>= 1）
```

## 测试

```sh
go test -race -v ./...
```

结果（Go 1.24，全部通过）：

```
--- PASS: TestWriteSkewDetected          # 验收场景：并发请假，恰好一人被中止
--- PASS: TestWriteSkewStress            # 200 轮压测，每轮恰好一人提交，不变量恒成立
--- PASS: TestSequentialSecondLeaveBlocked
--- PASS: TestNoFalsePositive            # 并发但不冲突（A 请假 + C 上岗）不误伤
--- PASS: TestAssertionImmediateFailure
--- PASS: TestFailedCommitRollsBack      # 回滚不污染已提交状态
--- PASS: TestDeleteAndReadYourWrites
--- PASS: TestConstraintErrorMessage
ok  	skewguard
```

## 用法示例

```go
db := skew.New()
db.RegisterConstraint("at-least-one-on-duty", func(v skew.View) bool {
    for _, kv := range v.Scan("doctor:") {
        if kv.Value == "on" {
            return true
        }
    }
    return false
})

tx := db.Begin()
_ = tx.Assert("at-least-one-on-duty", pred) // 声明本事务依赖的前提
tx.Set("doctor:A", "off")
err := tx.Commit() // 提交时重验；违反不变量则回滚并返回 *ConstraintError
```
