# Template Migration Verifier

可嵌入 TypeScript 宿主的旧模板实例热升级验证包。它不比较两个入口函数的返回状态，而是验证一张覆盖递归调用帧、返回点、编码器状态和已发布前缀的栈摘要迁移证书。

零第三方运行时依赖；Node.js `>=22.6` 可直接通过 strip-types 运行测试和样例。

## 核心结论

- 迁移只允许发生在旧新模板具有相同 `alignmentKey` 的完整输出边界。
- 认证收据绑定已发布字节哈希、逐段来源标签、词法上下文、编码器状态和栈摘要摘要。
- 栈摘要是有根正则图：普通帧、备选返回点和递归环都显式表示；验证器检查所有 root-to-top 相关帧，而不是只检查当前栈顶。
- 每个深层返回点都必须映射到保持词法上下文、关闭义务、来源绑定和编码器语义的新接续点。
- 编码器状态按迁移表和转移输出做等价检查；仅改状态名、把 `text` 映射到属性状态、或使用更弱编码器都会被拒绝。
- 信息缺失、收据没有绑定栈摘要、验证密钥缺失、摘要不完整等情况返回 `quarantined`；完整性或安全策略冲突返回 `denied`。

## 运行

```bash
npm test
npm run demo
node --experimental-strip-types src/cli.ts --json
```

当前包含 13 个测试，覆盖安全迁移、递归栈、返回点合并、入口状态对照、收据篡改、信息缺失隔离、状态重命名攻击和真实宿主输出。

## 嵌入流程

```ts
import {
  freezeDocument,
  HmacReceiptAuthenticator,
  runToBoundary,
  buildStackSummaryFromSession,
  stackSummaryHash,
  receiptDraftFromSession,
  issueCertificate,
  verifyMigration,
  migrateAndContinue,
} from "./src/index.ts";

const oldTemplate = freezeDocument(oldTemplateDocument);
const newTemplate = freezeDocument(newTemplateDocument);
const oldEncoders = freezeDocument(oldEncoderSet);
const newEncoders = freezeDocument(newEncoderSet);

const session = runToBoundary({
  template: oldTemplate.value,
  encoders: oldEncoders.value,
  data,
  boundaryId: "thread-boundary",
});

const stackSummary = buildStackSummaryFromSession(session, {
  summaryId: "stack-1",
  oldTemplateHash: oldTemplate.sha256,
  obligationByKind: {
    "commentItem:ci-after-replies": {
      requiredCloseTokens: ['" alt="avatar"></footer>', "</section>"],
    },
  },
});

const authenticator = new HmacReceiptAuthenticator(process.env.RECEIPT_KEY!);
const receipt = authenticator.authenticate(
  receiptDraftFromSession(session, {
    receiptId: "receipt-1",
    oldTemplateHash: oldTemplate.sha256,
    stackSummaryHash: stackSummaryHash(stackSummary),
  }),
);

const certificate = issueCertificate({
  certificateId: "cert-1",
  boundaryId: session.boundaryId!,
  alignmentKey: session.alignmentKey!,
  oldTemplate,
  newTemplate,
  oldEncoders,
  newEncoders,
  stackSummary,
  receipt,
  continuations: {
    // 每个相关栈帧都必须有映射，包括递归祖先的返回点。
  },
  encoderBindings: [
    {
      oldEncoderId: "html",
      oldStateId: "text",
      newEncoderId: "html",
      newStateId: "body",
    },
  ],
});

const result = verifyMigration({
  certificate,
  oldTemplate,
  newTemplate,
  oldEncoders,
  newEncoders,
  stackSummary,
  receipt,
  prefix: session.output,
  authenticator,
});

if (result.canMigrate) {
  migrateAndContinue({
    session,
    verification: result,
    newTemplate: newTemplate.value,
    encoders: newEncoders.value,
    data,
  });
} else {
  // 停止旧实例、保留取证、转入隔离渲染路径。
  quarantine(session.sessionId, result.findings);
}
```

## 判定语义

`verifyMigration` 返回三种结论：

- `safe`：所有哈希、收据、栈帧、尾部义务、来源标签、词法授权和编码器绑定均通过。
- `quarantined`：缺少密钥、栈摘要不完整、缺少某帧接续证明、收据未绑定栈摘要或验证器异常；宿主必须隔离该旧实例。
- `denied`：收据或冻结文档被篡改、边界不对齐、上下文改变、编码器重命名不等价、深层关闭义务丢失等确定性攻击/不兼容。

证书和所有输入都是 JSON 可序列化对象。证书可经 `JSON.stringify` / `JSON.parse` 跨进程传递后复验；可选 `signCertificate(cert, secret)` 为证书本身增加 HMAC。

## 证书证明内容

`MigrationCertificate` 绑定以下输入哈希：

- 旧模板和新模板；
- 旧编码器状态表集合和新编码器状态表集合；
- 栈摘要；
- 认证收据。

验证器随后重算：

1. 冻结文档哈希和模板 IR 引用完整性。
2. 收据 HMAC、前缀 SHA-256、分段连续覆盖和分段哈希。
3. 收据分段到冻结模板节点、`bindingId`、来源标签、词法上下文和编码器的溯源关系。
4. 栈摘要图的 root/top 可达性、收据调用链相容性和递归环。
5. 每个相关帧的新接续节点、上下文、活动来源绑定和有序关闭 token。
6. 从旧返回点和新接续点分别做尾部抽象分析，检查所有可达动态字段。
7. 旧字段到新字段的 `bindingId`、来源标签、授权上下文和编码器状态等价关系。
8. 返回点合并组中的每个旧返回点是否都有一致的新接续。

## 真实宿主样例

样例不是纯数据结构；`TemplateHost` 会实际执行调用、递归、字段编码、输出分段并生成收据。

| 样例 | 入口 | 对照结果 |
| --- | --- | --- |
| 两版共享调用片段 + 返回点合并 | `createMergeHarness()` | 两个旧返回点 `branch-a-rp`、`branch-b-rp` 均映射到 `merged-rp`；迁移后 A 的真实输出与旧版完整渲染完全相同 |
| 递归尾部跨 HTML/URL 编码边界 | `createThreadHarness(3)` | 栈为 `page -> commentItem RP -> commentItem RP -> boundary`；递归环和所有深层 RP 均在证书中 |
| 原版仍可安全完成 | `runOriginalThreadToCompletion()` | 旧宿主跑完全程，攻击字符串输出为 `&lt;img src=x onerror=...`，不存在原始 `<img src=x onerror=` |
| 只在深层调用发生的错误迁移 | `createDeepBadCertificate()` | 证书返回 `denied`；强制绕过验证时真实宿主输出 `EXPERIMENT-RAW:<img src=x onerror=...>`，证明检查点确实位于深层义务 |
| 缺少深层信息 | 删除一个 continuation 或构造不完整 `StackSummary` | 结果为 `quarantined`，不会按入口状态误放行 |

CLI 中的实际对照输出包括：

```text
thread verdict: safe
migrated == original host output: true
escaped script proof: true
encoded URL proof: true

deep bad verdict: denied
deep bad codes: ENCODER_RENAME_CONTEXT_MISMATCH, ENCODER_RENAME_SEMANTIC_MISMATCH,
FRAME_CONTEXT_RENAMED, NEW_TAIL_CLOSE_OBLIGATION_UNMET, ...
forced host raw attack: true

merge verdict: safe
merged output equals old A: true
```

## 主要模块

- `src/types.ts`：模板、编码器、收据、栈摘要和证书类型。
- `src/core.ts`：规范化 JSON、SHA-256、冻结文档和诊断类型。
- `src/encoders.ts`：真实编码、状态策略和重命名无关的状态等价检查。
- `src/templates.ts`：模板引用校验与递归尾部安全分析。
- `src/receipts.ts`：HMAC 收据、前缀和分段审计。
- `src/stack.ts`：正则栈图、递归环和收据调用链匹配。
- `src/certificate.ts`：证书签发与独立复验。
- `src/host.ts`：可执行模板宿主、会话迁移和栈摘要生成。
- `src/scenarios.ts`：五组对照样例。
- `test/migration.test.ts`：可执行规格和攻击回归测试。

## 安全边界

本包验证宿主提供的冻结 IR、收据和栈摘要；宿主仍需安全保管 HMAC 密钥、保证运行时数据来源标签真实，并确保模板注册后不可被运行实例修改。词法上下文由冻结模板中的上下文转换声明和编码器状态共同约束；如果接入真实 HTML/JS/URL parser，可以把额外词法 oracle 注入同一组 `Finding` 检查。
