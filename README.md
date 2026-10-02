# leasepool — 租约配额池与弹性借贷还款管理器

分布式并发任务许可证配额租约池（Go 标准库实现，无第三方依赖）。

## 能力

- 总配额固定（默认 100）的租约池，业务线按需 `Grant` 指定数量配额并承诺借期 `ttl`；
- 到期主动 `Return`（还款）释放配额；逾期归还追加违约惩罚审计；
- `Coordinator` 周期巡检，强制回收（Revoke）超期未归还的租约，并输出 `PENALTY` 违约惩罚审计日志；
- 配额不足时弹性调度：先回收超期租约，再由高优先级请求插队抢占低优先级在租租约（从最低优先级、最早到期者开始，仅抢占足够数量）；
- 全量审计日志（`GRANT` / `RETURN` / `REVOKE_EXPIRED` / `PREEMPT` / `PENALTY`），支持 `WithAuditHook` 外接落盘/上报。

## 运行样例

```sh
go run ./cmd/demo            # 业务 A 借 80 / 10s 借期，超期被吊销后高优业务 B 借 50
go run ./cmd/demo -ttl=2s    # 自定义借期
```

输出节选：

```
[2] 业务 A 到期未归还, 等待协调器强制回收 (Revoke)...
  AUDIT REVOKE_EXPIRED tenant=tenant-A lease=L0001 count=80 force-revoked: overdue 122ms beyond promised ttl
  AUDIT PENALTY        tenant=tenant-A lease=L0001 count=80 penalty: reason="lease expired without return" ...
[3] 高优业务 B 申请 50 个配额 (优先级 HIGH)
  AUDIT GRANT          tenant=tenant-B lease=L0002 count=50 priority=HIGH ...
```

## 测试

```sh
go test ./lease/ -v -count=1
go test ./lease/ -race -count=1
```

覆盖：验收边界（A 超期被吊销、配额转借高优 B、惩罚审计）、高优插队抢占、部分抢占、
同优先级配额不足、还款/逾期还款惩罚、非法参数、16 路并发借贷还款压力（配额永不越界）。
