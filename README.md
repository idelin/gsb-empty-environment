# lockmgr — 行级悲观锁与死锁预防器

Go 标准库实现的行级锁管理器，支持共享锁(S)/排他锁(X)、请求超时，
以及 **Wait-Die** / **Wound-Wait** 两种基于时间戳的死锁预防策略，
保证系统全局无死锁运行。

## 设计

- 事务 `Begin()` 时分配单调递增的时间戳 ID，**越小越老**。
- 锁兼容矩阵：`S+S` 兼容，其余组合冲突。
- 冲突时按策略比较时间戳：
  - **Wait-Die（非抢占）**：请求者比所有冲突持有者都老 → 等待；否则**自杀**（返回 `ErrAborted`）。
  - **Wound-Wait（抢占）**：请求者比冲突持有者老 → **抢占(Wound)** 对方（对方被中止需重启），自己立即获锁；否则等待。
- 等待通过全局广播通道唤醒重检，超时返回 `ErrTimeout`。
- 被中止/提交/回滚的事务自动释放全部锁并唤醒等待者。

## 运行测试

```sh
go test ./lockmgr/ -v
```

## 运行样例

```sh
go run ./cmd/demo
```

输出示例：

```
=== 场景 1: Wound-Wait —— 新事务请求老事务持有的 X 锁 → 挂起等待 ===
T1(id=1, 老)  T2(id=2, 新)
T1 持有 row-1 的 X 锁
T2 请求 row-1 的 X 锁 → 被挂起等待中...
T1 提交，释放锁
T2 被唤醒，获得锁，err = <nil>

=== 场景 2: Wound-Wait —— 老事务请求新事务持有的 X 锁 → 抢占(Wound) ===
T_old(id=1)  T_young(id=2)
T_young 持有 row-2 的 X 锁
T_old 请求 row-2 的 X 锁 → 直接抢占(Wound) T_young，立即获得锁
T_young 已被中止: true
T_young 以新事务身份重启，等待 T_old 释放锁...
T_old 提交后，重启的 T_young 获得锁，err = <nil>

=== 场景 3: Wait-Die —— 新事务遇到老事务持锁 → 自杀 ===
T1(老) 持有 row-3 的 X 锁
T2(新) 请求 row-3 的 X 锁 → 立即自杀, err = lockmgr: transaction aborted
```
