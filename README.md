# 分级访问控制组件（Tiered Access Control & Non-Repudiable Audit）

为数据平台实现的字段级分级访问控制组件，覆盖四项能力：

1. **按字段分级决定可见范围**：每个数据字段标注敏感级别，主体持读取/导出两级许可，访问时按级别裁剪可见字段。
2. **拒绝跨级读取**：读取请求中任一字段高于主体读取级别，整次请求拒绝，不返回任何受保护数据。
3. **拒绝批量导出组合**：导出组合中任一字段高于主体导出级别，整批导出拒绝（导出许可独立且不高于读取许可）。
4. **不可否认留痕与完整性校验**：每次访问（允许与拒绝均含）写入只追加日志，采用 SHA-256 哈希链 + Ed25519 数字签名，任何篡改、删除、重排或伪造均可被离线校验发现。

仅依赖 Node.js 内置模块（`node:crypto` / `node:fs` / `node:test`），无第三方依赖。

## 分级模型

`src/levels.ts` 定义四级（数值越大越敏感、可见范围越小）：

| 级别 | 含义 |
| --- | --- |
| `PUBLIC (0)` | 公开数据 |
| `INTERNAL (1)` | 内部数据 |
| `CONFIDENTIAL (2)` | 机密数据 |
| `SECRET (3)` | 绝密数据 |

主体（用户/服务账号）持有两个许可：

- `readClearance`：在线读取可见的最高级别。
- `exportClearance`：批量导出可用的最高级别，通常 `<= readClearance`。

## 模块结构

- `src/levels.ts`：分级常量与级别工具。
- `src/policy.ts`：`PolicyEngine` 纯判定引擎——字段可见性、`evaluateRead` 越级读取判定、`evaluateExport` 导出组合判定；`PolicyViolationError` 可选异常。
- `src/audit.ts`：`AuditLog` 不可否认留痕——哈希链、Ed25519 签名、只追加写入、重启续写、`verifyAuditFile` 离线完整性校验、`tamperAuditFile` 演练辅助。
- `src/access-control.ts`：`AccessControlService` 门面，串联策略判定与留痕，输出字段投影且拒绝时不泄露数据。
- `src/demo.ts`：可运行的端到端演示（含篡改演练）。
- `test/`：基于 `node:test` 的 20 个用例。

## 留痕安全模型

每条留痕为一行 JSON（JSONL），包含：

- `seq` 单调递增序号、`timestamp`、`entryId`（UUID）、主体、动作、资源、请求字段、判定结果与原因、越级字段、来源 `origin`。
- `prevHash`：前一条目的 `entryHash`（创世条目为固定 `GENESIS_PREV_HASH`），形成哈希链。
- `entryHash`：对条目内容（规范化、字典序序列化后）计算的 SHA-256。
- `signature`：平台 Ed25519 私钥对 `entryHash` 的签名。

完整性校验（`verifyAuditFile`）逐行检查：JSON 可解析 → 序号连续 → `prevHash` 链接正确 → 内容摘要匹配 → 签名通过公钥验签。

- 篡改历史条目内容 → `DIGEST_MISMATCH`
- 删除/重排条目 → `BAD_SEQUENCE` 或 `BROKEN_CHAIN`
- 用他人私钥伪造签名 → `SIGNATURE_INVALID`
- 损坏日志行 → `MALFORMED_LINE`

生产部署时签名私钥应放入 KMS/HSM，审计方仅持有公钥即可独立验签，实现平台与访问者双方不可否认。

## 运行

```bash
npm test          # 运行全部 20 个测试用例
npm run demo      # 端到端演示，留痕输出到 logs/audit-demo.jsonl
```

## 快速上手

```ts
import {AuditLog, generateAuditKeyPair} from './src/audit.ts';
import {AccessControlService} from './src/access-control.ts';
import {Level} from './src/levels.ts';

const keyPair = generateAuditKeyPair();
const service = new AccessControlService(
  {id: Level.PUBLIC, email: Level.CONFIDENTIAL, bankAccount: Level.SECRET},
  AuditLog.open('logs/audit.jsonl', keyPair),
);

const subject = {id: 'u-1', name: 'analyst', readClearance: Level.CONFIDENTIAL, exportClearance: Level.INTERNAL};
const outcome = await service.readRecord(subject, 'customer/c-1', record, ['id', 'bankAccount']);
// outcome.decision.allowed === false，outcome.data === null，拒绝留痕已签名落盘
```
