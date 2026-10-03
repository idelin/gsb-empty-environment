# ssi — 可串行化快照隔离（SSI）防异常器

纯 Go 标准库实现的内存 KV 库，在快照隔离（SI）之上实现
Serializable Snapshot Isolation，精准检测并阻止写倾斜（Write Skew）异常。

## 算法

- **MVCC 快照隔离**：每个事务在一致的快照上读取；写写冲突按
  first-committer-wins 在提交时中止后提交者。
- **SIREAD 锁**：读操作对可见版本加 SIREAD 锁（不阻塞写），
  用于发现“我读到的版本被并发事务覆写”。
- **rw-antidependency 冲突图**：
  - 写 `Set`/`Commit` 时，对当前版本上每个并发 SIREAD 读者 `R`
    建立边 `R -rw-> W`；
  - 读 `Get` 时若可见版本已被快照后的提交覆写，建立边
    `读者 -rw-> 覆写者`。
- **危险结构检测**：新增边 `a -rw-> b` 时，检查是否形成
  `T_in -rw-> T_pivot -rw-> T_out`（含二元环）。一旦出现，立即中止
  环上仍活跃的事务（优先枢纽），在异常发生前夕打破环，保证完全可串行化。

## 验收场景

A=100, B=100，约束 A+B>=0；T1 读 A、B 扣 A 80；T2 并发读 A、B 扣 B 80。
纯 SI 下两事务都基于 sum=200 的旧快照提交（写倾斜）；SSI 在 T2 写 B 时
识别出 `T1 -rw-> T2 -rw-> T1` 危险结构，精准中止 T2。

## 运行

```sh
go test ./... -v   # 测试（含验收场景与纯 SI 对照）
go run ./cmd/demo  # 运行样例：SI vs SSI 对比
```

## 限制

演示性质：不处理幻影（phantom）/谓词读；已提交事务的 SIREAD 锁不做回收。
