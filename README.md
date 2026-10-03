# 动态排班技能资质时态匹配器

轮班调度与岗位资质的**双时态**匹配器（Go，仅标准库）：

- **有效时间（valid time）**：证书的生效期 / 复审期，以及吊销实际生效的时间点；
- **知悉时间（knowledge time）**：系统何时得知吊销通知。

排班时校验班次整个时间段是否被员工资质**持续覆盖**；追溯吊销时自动回扫已排班次，输出违规预警列表。

## 结构

- `scheduler.go` — 核心类型与匹配逻辑（`Certificate` / `Shift` / `Scheduler` / `Revocation`）
- `interval.go` — 半开区间 `[From, To)` 的差集运算（计算缺资质区间）
- `scheduler_test.go` — `go test` 测试，覆盖全部验收边界
- `cmd/demo/main.go` — 运行样例

## API 摘要

```go
s := scheduler.NewScheduler()
s.AddCertificate(scheduler.Certificate{ID: "C1", EmployeeID: "E1", Skill: "welding", ValidFrom: 10, ValidTo: 100})
err := s.Schedule(scheduler.Shift{ID: "S1", EmployeeID: "E1", Skill: "welding", Start: 80, End: 120}, knowledgeNow)
// err 为 *scheduler.GapError，Gaps == [{100, 120}]
violations, _ := s.Revoke("C1", effectiveAt /*有效时间*/, knownAt /*知悉时间*/)
// violations 为因此违规的已排班次预警列表
```

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 样例
```

## 验收边界对应

| 场景 | 测试 |
| --- | --- |
| 持证 [10,100)，排班 [80,120) → 检出 [100,120) 缺资质并拒绝 | `TestScheduleRejectsUncoveredTail` |
| 半开区间边界 [10,100) 恰好覆盖 → 通过 | `TestScheduleExactBoundaryAccepted` |
| 知悉时间收到通知：证书在 60 吊销 → 预警已排班次 | `TestRetroactiveRevocationAlertsScheduledShifts` |
| 知悉时间之前的视角不受吊销影响（双时态） | `TestRevocationInvisibleBeforeKnowledgeTime` |
