# schedenum — 协作式调度枚举器

枚举最多 4 个协程在**有界调度步数**内的所有不同调度，找出违反断言
（或死锁）的**最短事件序列**。

## 模型

- 协程只在线性指令流中的**显式调度点**让出：`Load` / `Store` / `Wait`。
  其余指令（`Assign`、`Jmp`、`JmpIf`、`Notify`、`Assert`）在到达下一个
  调度点之前原子地连续执行。
- `Wait(cv)` 使线程阻塞，直到某个线程执行 `Notify(cv)`；`Notify` 只唤醒
  当前正在等待的线程（先 notify 后 wait 即丢失唤醒）。
- 共享内存为定长 `[]int`，每个线程有独立的寄存器组。

## 状态合并的正确性

分支按完整状态合并，合并键包含：

- 共享内存全部单元；
- 每个线程的 PC 与寄存器；
- 每个线程的**等待状态**（阻塞在哪个条件变量、是否有待处理的唤醒）。

最后一条是关键：两个内存与 PC 完全相同、但等待状态不同的状态，其
**可运行性集合不同**，绝不能合并——否则会漏掉死锁或丢失可运行分支
（见 `sched/explore_test.go` 的 `TestWaitStatePreservedByMerge`）。

## 使用

```sh
go test ./sched/        # 运行正确性测试
go run ./cmd/schedenum  # 运行两个内置反例（丢失唤醒死锁、非原子计数器）
```

输出为违反断言/死锁的最短调度轨迹，例如：

```
== lost wakeup (deadlock): explored 13 states, merged 3 duplicates
   VIOLATION (deadlock): deadlock: every live thread is blocked in wait
   shortest schedule (4 events):
     1. T0  r0 := load mem[0] (= 0)
     2. T1  store mem[0] := 1
     3. T1  notify cv0 (woke 0)
     4. T0  wait cv0 (blocks)
```

## API 概览

- `sched.Machine`：线程程序（`[]Instr`）、内存与寄存器规模。
- `sched.Explore(m, Options{MaxSteps, MaxStates, Visit})`：BFS 枚举，
  返回 `Result{Violation, Explored, Merged, Truncated}`。
- `Machine.Replay(trace)`：重放反例轨迹，验证可复现。
