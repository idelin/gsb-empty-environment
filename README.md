# 分层仲裁多数派决策引擎 (hierquorum)

多数据中心分级 Quorum 仲裁器，纯 Go 标准库实现。

## 设计

- **分层结构**：每个区域(IDC)拥有独立权重与本地多数派门限
  (`LocalThreshold`)。区域内确认数未达门限时，该区域的投票无效。
- **正常拓扑**：全部可达区域参与，按全局权重过半裁决。
- **降级拓扑**：主中心不可达时自动降级，剩余可达区域按自身权重
  构成临时多数派，避免跨区网络闪断引起全局不可用；降级期间
  主中心被隔离，不得投票。
- **纪元围栏**：主中心恢复后须通过 `Takeover(epoch)` 出示**更高**
  纪元编号才能重新接管；旧纪元提案一律被围栏拒绝，同纪元冲突值
  被拒绝，保证无状态冲突。
- **签名证据**：每张投票由区域密钥 HMAC-SHA256 签名，整份决议由
  仲裁器证据密钥签名，`Verify` 可独立校验投票签名、拓扑门限与
  证据完整性。

## 目录

- `quorum/quorum.go` — 仲裁引擎核心
- `quorum/quorum_test.go` — 验收测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 验收场景

拓扑：IDC-A 权重 2(主中心)，IDC-B / IDC-C 权重各 1。

1. 正常拓扑总权重 4、门限 3：A+B 达成多数派；B+C(权重2) 不够。
2. A 失联 → 自动降级，参与权重 2、门限 2：B+C 达成临时多数派。
3. A 恢复：`Takeover` 须出示更高纪元(旧纪元被拒绝)，接管后恢复
   正常拓扑继续裁决，旧纪元写被围栏，无状态冲突。

## 测试结果

```
=== RUN   TestNormalTopologyQuorum
--- PASS: TestNormalTopologyQuorum (0.00s)
=== RUN   TestDegradedTopologyOnPrimaryLoss
--- PASS: TestDegradedTopologyOnPrimaryLoss (0.00s)
=== RUN   TestPrimaryRecoveryTakeoverNoConflict
--- PASS: TestPrimaryRecoveryTakeoverNoConflict (0.00s)
=== RUN   TestSameEpochConflictingValueRejected
--- PASS: TestSameEpochConflictingValueRejected (0.00s)
=== RUN   TestLocalMajorityGate
--- PASS: TestLocalMajorityGate (0.00s)
=== RUN   TestUnreachableVoterRejected
--- PASS: TestUnreachableVoterRejected (0.00s)
=== RUN   TestEvidenceTamperingDetected
--- PASS: TestEvidenceTamperingDetected (0.00s)
=== RUN   TestTakeoverRequiresReachablePrimary
--- PASS: TestTakeoverRequiresReachablePrimary (0.00s)
PASS
ok  	hierquorum/quorum
```
