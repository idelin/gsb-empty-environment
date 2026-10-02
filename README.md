# 动态排班技能资质时态匹配器

轮班调度与岗位资质的**双时态**匹配器（Go，仅标准库）：

- **有效时间（valid time）**：证书生效期 `[ValidFrom, ValidTo)`、复审期截止 `ReviewDue`、追溯吊销生效点 `RevokedAt`。
- **事务时间（transaction time / 知悉时间）**：系统收到吊销通知的时间 `RecordedAt`。

证书实际有效截止点 = `min(ValidTo, ReviewDue, RevokedAt)`。

## 核心能力

- `Schedule(shift)`：排班时验证班次整段时间内资质**持续有效**（支持多证书拼接覆盖）；
  缺资质则拒绝并返回缺口时段列表。
- `RevokeCertificate(certID, revokedAt, recordedAt)`：知悉时间收到追溯吊销通知后，
  自动扫描已排班次，输出受影响班次的违规预警列表（含失去覆盖的具体时段）。

## 文件

- `matcher.go` — 区间、证书、班次、调度器与缺口计算
- `matcher_test.go` — 验收边界测试（`go test`）
- `main.go` — 运行样例

## 运行

```sh
go test ./... -v   # 测试
go run .           # 演示样例
```

## 验收边界对应测试

| 边界 | 测试 |
|---|---|
| 持证 [10,100)，排班 [80,120) → 检出 [100,120) 缺资质并拒绝 | `TestScheduleRejectsUncoveredTail` |
| 班次完全在有效期内 → 排班成功 | `TestScheduleAcceptsFullyCovered` |
| 知悉时间收到证书在 60 吊销 → 自动预警已排班次违规 | `TestRetroactiveRevocationWarnsScheduledShifts` |
