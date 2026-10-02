# Immutable Append-Only Audit Ledger

单机离散时间、不可变只追加的事务审计账簿（TypeScript，零依赖，Node.js 内置模块）。

## 特性

- **哈希链**：每个区块包含前序区块哈希（`prevHash`），创世哈希为 64 个 `0`。
- **业务有效载荷**：转账事务（from / to / amount / asset / memo）。
- **签名回执**：每个区块由账簿签名者使用 Ed25519 对规范序列化内容签名，`verifyChain()` 全量校验哈希链与签名。
- **离散时间**：内置逻辑时钟，每追加一个区块递增 1。
- **局部撤销（冲正）**：`reverse(index)` 生成逆向冲正条目（`kind=REVERSAL`，`reversalOf` 指向原区块），只追加、绝不物理覆写历史区块；重复冲正、冲正非转账区块均抛错。
- **默克尔路径证明**：对任意历史区块生成 Merkle 路径证明；账簿记录每个高度的根哈希（`getRoot(height)`），冲正前生成的旧证明对历史高度根仍然有效。
- **不可变接口**：`getBlock()` 返回冻结副本，外部无法通过返回值篡改账簿状态。

## 环境要求

Node.js >= 22.6（利用内置 TypeScript type-stripping 与 `node:test`，无需安装任何依赖）。

## 运行

```bash
npm run demo   # 运行样例：100 笔转账 + 冲正第 50 笔 + 证明验证
npm test       # node:test 内置测试
```

## 样例输出（`npm run demo`）

```
=== 1. Append 100 transfer transactions ===
height            : 100
logical clock     : 100
chain valid       : true
merkle root @100  : 979fc85d...a267c9
alice balance     : -500
bob balance       : 500

=== 2. Merkle proof for block #50 (before reversal) ===
proof path length : 7
proof verifies    : true

=== 3. Reverse transaction #50 (compensating entry) ===
reversal block    : #101 (kind=REVERSAL, reversalOf=50)
reversal payload  : bob -> alice 500 COIN
height            : 101
chain valid       : true
alice balance     : 0
bob balance       : 0

=== 4. History is immutable: block #50 evidence still valid ===
block #50 unchanged      : true
block #50 marked reversed: true
old proof vs root@100    : true
new proof vs root@101    : true
reversal proof vs @101   : true

=== 5. Tamper check: forged proof must fail ===
forged proof verifies    : false
```

## 测试结果（`npm test`，8/8 通过）

```
✔ appending 100 transfers builds a valid hash chain
✔ every block carries a verifiable signature receipt
✔ reversing #50 appends compensating block #101 and updates balances
✔ reversal never overwrites history: block #50 evidence stays intact
✔ merkle proofs are available for every historical block
✔ tampered proofs and tampered history are rejected
✔ double reversal and invalid operations are rejected
✔ ledger state is not mutable through returned blocks
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

## 代码结构

- `src/crypto.ts` — SHA-256、规范 JSON 序列化、Ed25519 签名/验签
- `src/merkle.ts` — Merkle 树构建、路径证明生成与验证
- `src/ledger.ts` — 只追加账簿：哈希链、签名回执、冲正、余额、历史根证明
- `src/demo.ts` — 验收场景运行样例
- `test/ledger.test.ts` — Node.js 内置测试（node:test + node:assert）
