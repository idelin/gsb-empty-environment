# Immutable Audit Ledger（不可变只追加事务审计账簿）

单机、离散时间的审计日志账簿，TypeScript 实现，零外部依赖（仅 Node.js 内置模块）。

## 特性

- **哈希链**：每个区块包含 `prevHash`，指向前序区块，篡改任何历史区块都会破坏链条（`verifyChain()`）。
- **业务有效载荷**：转账事务（`transfer`）携带 from/to/amount/memo。
- **签名回执**：账簿使用 Ed25519 私钥对每个区块哈希签名，回执随区块存储，可用公开密钥独立验签。
- **局部撤销（冲正）**：`reverse(txId)` 追加一条逆向冲正条目（`reversal`），资金回滚并更新总账；历史区块永不物理覆写（深度 `Object.freeze`，严格模式下赋值抛 `TypeError`）。
- **默克尔路径证明**：`getProof(index, atHeight)` 可对任意历史高度生成默克尔路径，`Ledger.verifyProof` 离线验证（区块哈希 + 默克尔根 + 签名回执）。
- **离散时间**：每个区块携带严格递增的逻辑时钟 `tick`。

## 运行环境

Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 .ts，无需编译）。推荐 Node 23+。

## 使用

    npm test     # node --test，运行内置测试
    npm run demo # 运行验收样例：100 笔转账 + 冲正第 50 笔

示例代码：

    import { Ledger } from './src/ledger.ts';

    const ledger = new Ledger({ initialBalances: { alice: 1000, bob: 1000 } });
    ledger.submitTransfer({ from: 'alice', to: 'bob', amount: 100 });
    ledger.reverse('TX-0001', 'duplicate charge');

    const proof = ledger.getProof(0, 1);           // 第 1 个区块在历史高度 1 下的证明
    Ledger.verifyProof(proof, ledger.publicKeyPem); // true

## 目录结构

- `src/ledger.ts` — 账簿核心：区块、哈希链、Ed25519 签名回执、冲正、总账
- `src/merkle.ts` — 默克尔树：根计算、路径生成与验证
- `test/ledger.test.ts` — node:test + node:assert 内置测试
- `examples/demo.ts` — 验收运行样例
