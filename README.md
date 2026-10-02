# SSI — Serializable Snapshot Isolation 防异常器

基于 Cahill 等人的 SSI 算法（SIGMOD 2008，PostgreSQL 9.1+ 采用）在 MVCC
快照隔离之上实现完全可串行化，仅使用 Go 标准库。

## 原理

1. **快照隔离（SI）**：每个事务在 `Begin` 时获得一致性快照；
   写-写冲突按 first-committer-wins 处理。
2. **SIREAD 锁**：每次读都在键上放置 SIREAD 锁，事务提交后仍保留，
   直到不存在与之重叠的活动事务。
3. **rw-反依赖边**：若 T_r 读过某键，而并发事务 T_w 覆写该键
   （无论写在读之前还是之后，包括未提交的写），记录边
   `T_r -rw-> T_w`。
4. **危险结构检测**：串行化图中任意 rw 环都包含危险结构
   `T1 -rw-> T2 -rw-> T3`（T3 最先提交，可与 T1 相同）。在提交时或
   加边时检测到该结构即中止事务：中枢 T2 仍活跃则中止 T2，
   否则中止当前提交者——在写倾斜异常落盘前精准拦截。

## 验收场景（写倾斜）

账户 A=100、B=100，约束 A+B>=0。T1 读 A、B 并扣 A 80；T2 并发读
A、B 并扣 B 80。纯 SI 下两者都提交（各自看到 A+B=200），产生写倾斜；
SSI 识别出危险结构 `T1 -rw-> T2 -rw-> T1` 并中止中枢事务 T2。

## 运行

```sh
go test ./... -v   # 单元测试（含验收场景 TestWriteSkewPrevented）
go run ./cmd/demo  # 运行样例
```

## 文件

- `ssi.go` — MVCC 存储 + SIREAD 锁 + rw-反依赖图 + 危险结构中止
- `ssi_test.go` — 验收场景、反向提交顺序、无误报、写-写冲突、
  只读事务、三事务长链等测试
- `cmd/demo/main.go` — 写倾斜演示程序
