# 分层仲裁多数派决策引擎 (Hierarchical Quorum Arbiter)

多数据中心分级 Quorum 仲裁器：各区域（IDC）拥有独立权重与本地多数派门限；
主中心不可达时动态降级为**降级仲裁拓扑**，由幸存区域构成临时多数派继续提交，
避免跨区网络闪断引起全局不可用；所有仲裁结果携带完整签名证据（Ed25519）。

纯 TypeScript 实现，零依赖，使用 Node.js 内置测试运行器（Node ≥ 23.6 原生类型擦除）。

## 核心概念

- **分层拓扑（hierarchical）**：全局加权多数派 `floor(Σw / 2) + 1`，且主中心必须投赞成票（层级否决规则）。
- **降级拓扑（degraded）**：主中心失联时自动降级并递增纪元（epoch）；法定权重基数切换为
  全部非主中心配置权重，幸存区域达成临时多数派即可提交，单区域无法自成法定人数（防脑裂）。
- **本地多数派门限**：每个区域内部按 `localApprovals / replicaCount > localThreshold` 判定本地多数派。
- **纪元接管**：主中心恢复后须出示**更高纪元**方可重新接管，并同步降级期间已提交的决策日志；
  过期纪元被拒绝，杜绝状态冲突。
- **签名证据**：每张选票由区域 Ed25519 私钥签名，仲裁结果由仲裁器私钥签名，
  `verifyDecision()` 可独立重放校验（签名、计票、法定规则）。

## 运行

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 验收场景（examples/demo.ts）

1. IDC-A(w=2, 主中心) + IDC-B(w=1) + IDC-C(w=1)，法定权重 3，正常提交。
2. IDC-A 失联 → 自动降级、epoch 1→2，B+C 以临时多数派（2/2）提交 `txn-002`。
3. IDC-A 恢复并出示 epoch 3 重新接管，同步已提交日志，恢复分层拓扑。
4. 过期纪元（≤ 当前）的接管请求被拒绝，无状态冲突。

## 结构

- `src/arbiter.ts` — 仲裁器：拓扑切换、法定人数计算、纪元管理、证据校验
- `src/region.ts` — 区域：权重、本地多数派门限、投票签名
- `src/crypto.ts` — Ed25519 签名 / 验签、规范化序列化、SHA-256
- `src/types.ts` — 选票 / 决策 / 计票类型
- `test/arbiter.test.ts` — 9 个验收用例（node:test）
- `examples/demo.ts` — 端到端运行样例
