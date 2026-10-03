# Erasable Compliance Data-Redaction Prover

可擦除合规数据脱敏证明器：基于 Merkle 树的存证账本，支持满足"数据被遗忘权"的
**可验证擦除**。当客户注销并要求删除个人记录时，系统将该叶子重写为带删除签名的
`Erase_Marker`，树根哈希平滑演进，其余未注销客户的存证路径依然完全有效可验。

## 设计

- **叶子承诺**：`leaf = H("COMPLIANCE_LEAF/v1" ‖ recordId ‖ H(data))`，域分离防混淆。
- **内部节点**：`node = H("COMPLIANCE_NODE/v1" ‖ left ‖ right)`。
- **擦除标记**：合规官使用 Ed25519 私钥对擦除请求
  `H_request(recordId ‖ originalLeafHash ‖ requestedAt)` 签名，叶子被重写为
  `Erase_Marker = H("COMPLIANCE_ERASE_MARKER/v1" ‖ recordId ‖ originalLeafHash ‖ signature)`。
- **物理擦除**：明文以 `Buffer` 保存，擦除时先逐字节置零再从存储中移除。
- **可验证擦除证明**：`ErasureProof` 携带删除签名、合规官公钥、原叶子哈希、
  擦除标记与 Merkle 路径，任何第三方可独立验证：
  1. 删除签名确实由合规官针对该记录的擦除请求签发；
  2. 擦除标记正确承诺了原叶子与签名；
  3. 擦除标记包含在当前树根中。
- **平滑演进**：仅被擦除叶子到根的路径发生变化，其余叶子及其兄弟子树不变，
  因此其余记录针对新根的存证证明照常生成与验证；历史证明仍可针对历史根验证
  （完整审计轨迹保存在 `ledger.history`）。

## 运行环境

- Node.js ≥ 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，
  零第三方依赖；测试使用内置 `node:test`）。

## 运行

```bash
npm test        # 运行内置测试
npm run demo    # 运行演示样例
npm run typecheck  # 若本机装有 typescript，可做类型检查
```

## 演示输出（`npm run demo`）

```
=== 1. Onboard customers (append records) ===
ledger root: 0678f26909e8990e412b9959ffa939bd19e38b9f4bbacb480932c52424fe8d94

=== 2. Prove inclusion for cust-002 ===
proof path length: 2
verifies against root: true

=== 3. cust-003 exercises the right to be forgotten ===
root before erasure: 0678f26909e8990e412b9959ffa939bd19e38b9f4bbacb480932c52424fe8d94
root after erasure : ea1b43bb6e32c4d4707398dc5bf2e172b0e1841265ec64a6b05810f9e85138c2
erase marker hash  : 44320d526e5add63a54ecafb6e5a03961164a5986a2188e8d057bbc5d36ce150
deletion signature : c53965368222db8b0a36a24d47dab573...
plaintext retained?: false

=== 4. Anyone can verify the erasure proof ===
erasure proof valid: true

=== 5. Remaining customers' proofs still verify against the NEW root ===
  cust-001 valid: true
  cust-002 valid: true
  cust-004 valid: true

=== 6. Root evolution history (smooth audit trail) ===
  epoch 0 [append] cust-001 -> b6f631793ad57003...
  epoch 1 [append] cust-002 -> 7b6292e64628f797...
  epoch 2 [append] cust-003 -> e3ea29f52ed3750c...
  epoch 3 [append] cust-004 -> 0678f26909e8990e...
  epoch 4 [erase ] cust-003 -> ea1b43bb6e32c4d4...
```

## 测试结果（`npm test`，Node v22.14.0）

```
ok 1 - inclusion proofs verify for all records against current root
ok 2 - erasure replaces leaf with Erase_Marker and root evolves smoothly
ok 3 - original customer data is physically destroyed after erasure
ok 4 - other customers' proofs remain fully valid against the new root after erasure
ok 5 - historical proofs still verify against historical roots (audit trail)
ok 6 - erasure proof is publicly verifiable: signature + marker + merkle path
ok 7 - tampered erasure proofs are rejected
ok 8 - tampered inclusion proofs are rejected
ok 9 - double erasure and unknown records are rejected
ok 10 - multiple erasures keep remaining proofs valid
ok 11 - leaf hash commits to record id and data hash
ok 12 - root history records every append and erase epoch

# tests 12
# pass 12
# fail 0
```

## 代码结构

- `src/hash.ts` — 域分离的 SHA-256 承诺与擦除标记/擦除请求构造。
- `src/tree.ts` — Merkle 树构建、包含证明生成与验证。
- `src/ledger.ts` — `ComplianceLedger`（记录上链、可验证擦除、物理销毁、根历史）
  及 `verifyInclusionProof` / `verifyErasureProof` 公开验证函数。
- `test/ledger.test.ts` — 12 个 `node:test` 用例，覆盖全部验收边界。
- `examples/demo.ts` — 端到端演示：上链 → 存证 → 注销擦除 → 第三方验证。
