# 实际运行结果

命令：`node --test test/*.test.ts`（Node.js v23.9.0，原生 TypeScript 类型擦除 + 内置测试运行器）

```
v23.9.0
2026-09-25 21:44:30 CST
(node:57093) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ two concurrent evaluations share one fetch; cancelling one does not affect the other (70.976417ms)
✔ aborting before the first flush means no fetch happens at all (35.27575ms)
✔ cache merges reads for the same subject+permission and isolates different permissions (35.351625ms)
✔ reentrant fetcher calls are serialized into a later batch, after the in-flight one (15.015334ms)
(node:57094) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
enumeration baseline: { ABC: 11, ACB: 111, BAC: 10, BCA: 10, CAB: 111, CBA: 110 }
heuristic cost=11, enumerated min=10, max=111
✔ full enumeration of fetch orders gives a cost baseline; the heuristic claims no optimum (33.306709ms)
(node:57095) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ fetch timeout rejects deterministically and a later evaluation recovers (50.021958ms)
✔ mixed-version reads trigger a retry and converge on a consistent version (1.008417ms)
✔ version churn beyond the retry budget fails deterministically (0.782125ms)
✔ partial batch failure: healthy attributes serve, failed ones reject deterministically (0.441375ms)
(node:57096) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ deny decided by a cheap false condition never fetches the sensitive expensive attribute (6.232125ms)
✔ policy validation rejects undeclared attributes and non-integer conditions (1.239416ms)
ℹ tests 11
ℹ suites 0
ℹ pass 11
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 2073.511458
exit=0
```

## 结果摘要

- 11 个测试全部通过，exit=0。
- 短路：敏感昂贵属性 `ssn` 在 `age` 已决定拒绝时 0 次请求。
- 并发：两次判断合并为一次 `x` 读取；取消其一不影响另一个，且取消后不再取 `y`。
- 取值器：重入被序列化到后续批次（`[[a],[a2]]`）；超时确定拒绝且后续可恢复；版本切换触发 1 次重试并收敛到单一版本；持续版本抖动在重试预算内确定失败；部分批失败只影响失败属性。
- 成本基准：6 种获取顺序全枚举 `{ABC:11, ACB:111, BAC:10, BCA:10, CAB:111, CBA:110}`，启发式成本 11，枚举最小 10、最大 111；启发式不宣称全局最小（`claimsOptimal === false`）。
