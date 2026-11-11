# 锁次序与条件依赖

可导入的 Go 分析包（包名 `netsec`，模块 `example.org/netsec/uid1237`），在两个有限状态线程、最多六把非重入互斥锁上寻找可达死锁。

## 模型

- `Spec` 包含两个 `Thread` 与 `Locks`（1–6 把）。每个线程是有限状态机，状态上没有可用边即表示正常退出。
- `Edge` 携带锁操作 `Op`（`OpNone`/`OpLock`/`OpUnlock`）和可选布尔条件 `Guard`（`Var`、`Not`、`And`、`Or`，`nil` 表示恒真）。
- 同名条件变量在整次执行中固定：分析器对出现在边上的所有条件变量枚举真值赋值，每个赋值独立做一次具体状态遍历。

## 分析方法

1. 校验并编译输入：状态/锁/条件建索引，条件表达式编译成按位求值的内部形式。
2. 对每个固定条件赋值做 BFS，状态键为 `(两线程程序计数器, 每把锁的持有者)`。
3. 只展开在当前具体状态下条件成立、且锁可立即获取的边；被拒绝的加锁请求不是一次转移。
4. 当所有线程都无法前进且至少一个线程阻塞时分类死锁：
   - `DeadlockRecursive`：线程再次请求自己已持有的非重入锁（等待图自环）。
   - `DeadlockCycle`：阻塞线程构成等待环，包括经典 A→B / B→A。
   - `DeadlockOrphaned`：阻塞请求的持锁线程已退出，请求永远无法被授予。
5. BFS 保证返回执行边数最少的交错轨迹；循环/递归死锁优先于同图中更浅的孤儿等待。互斥分支上的加锁边从不在同一赋值下同时成立，因此不会被静态拼成假环。

## 用法

```go
import "example.org/netsec/uid1237"

spec := netsec.Spec{
    Locks: []string{"A", "B"},
    Threads: [2]netsec.Thread{
        {
            Name: "T0", Start: "0", States: []string{"0", "1", "2"},
            Edges: []netsec.Edge{
                {Name: "lockA", From: "0", To: "1", Op: netsec.OpLock, Lock: "A"},
                {Name: "lockB", From: "1", To: "2", Op: netsec.OpLock, Lock: "B"},
            },
        },
        {
            Name: "T1", Start: "0", States: []string{"0", "1", "2"},
            Edges: []netsec.Edge{
                {Name: "lockB", From: "0", To: "1", Op: netsec.OpLock, Lock: "B"},
                {Name: "lockA", From: "1", To: "2", Op: netsec.OpLock, Lock: "A"},
            },
        },
    },
}

res, err := netsec.Analyze(spec)
// res.Reachable == true, res.Kind == netsec.DeadlockCycle
// res.Trace 为最短轨迹，res.Assignment 给出条件变量的固定真值
```

## 核对的图

`go test ./...` 覆盖：互斥分支不拼环、经典 AB-BA 循环及最短轨迹、递归请求同一锁、正常退出无死锁、条件守卫下无可行环、仅在特定赋值下成立的死锁、持锁者退出导致的孤儿等待，以及输入校验和解锁未持有的错误。
