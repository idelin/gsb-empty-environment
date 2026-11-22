中继集群允许参与方断线换节点，不能让旧节点上未消费的应答被交给新连接里的另一个人。写供 TypeScript 中继宿主调用的会话协调包。一次会谈包含发起者、接收者、内容组、双方连接代次、唯一会谈号及各自签过名的同意范围；转发应答必须绑定最初邀请的摘要。连接迁移可以接管原会话，但需要旧连接释放、新连接承接和对端认可，三个事实各有签名且可乱序；只有由同一迁移事务绑定才能合并。签名身份与外部显示名称分开，重用显示名称、传输地址或会谈号均不继承同意。对部分日志求当前可投递对象、不可投递或缺少哪些事实，重复回调不多发。持久化存储只有按键比较交换，投递端提供可查询的幂等收件回执；恢复必须区分已投递与尚无确定结果。使用两个节点的完整事件回放参考，比对异步接管、交叉邀请、撤销后迟到确认、网络收件方已收到而中继丢失响应及同一参与者使用两个组。

## 交付内容

- `src/types.ts` 领域模型与判定类型
- `src/canonical.ts` 规范化 JSON、邀请摘要、签名输入
- `src/crypto.ts` Ed25519 签名身份与事实签名/验签
- `src/store.ts` 仅支持按键比较交换的存储接口与内存实现
- `src/receipts.ts` 可查询、幂等且绑定接收身份的收件回执
- `src/migration.ts` 迁移三事实乱序合并与同事务绑定规则
- `src/session.ts` 事件溯源状态机、事实校验、投递判定、恢复分类
- `src/coordinator.ts` 中继宿主调用的持久化协调器（去重回调）
- `src/replay/` 两个节点的完整事件回放参考与五个规定场景
- `test/` 25 个 `node:test` 用例；`doc/design.md` 设计说明

## 运行

```bash
npm test                 # 全部单元测试与回放参考断言
npm run replay:reference # 打印五个双节点回放场景的逐条比对
```

## 最小用法

```ts
import {
  Ed25519SignatureService,
  InMemoryCasStore,
  CasReceiptDirectory,
  SessionCoordinator,
  buildInvitation,
  buildConsentScope,
  digestOf,
} from "./src/index.ts";

const sig = new Ed25519SignatureService();
const alice = sig.generateIdentity();
const bob = sig.generateIdentity();
const store = new InMemoryCasStore();
const coordinator = new SessionCoordinator({
  nodeId: "node-A",
  store,
  signatures: sig,
  receipts: new CasReceiptDirectory(store),
  notifier: { onVerdict: (verdict) => console.log(verdict) },
});

const invitation = buildInvitation({
  sessionId: "session-1",
  initiator: alice.identity,
  receiver: bob.identity,
  group: "group-1",
  initiatorGeneration: 1,
  nonce: "nonce-1",
  createdAt: Date.now(),
});
const inviteFact = sig.signFact(alice, "invitation", invitation);
await coordinator.submitFact("session-1", inviteFact);

const scope = buildConsentScope({
  invitation,
  party: alice.identity,
  generationAtConsent: 1,
  actions: ["relay-answer", "migrate-connection"],
  notBefore: 0,
  expiresAt: Date.now() + 600_000,
});
await coordinator.submitFact("session-1", sig.signFact(alice, "consent", scope));

const verdict = await coordinator.queryVerdict("session-1", "answer-1");
// verdict.status 为 deliverable | waiting | blocked | in-flight | delivered
console.log(digestOf(invitation), verdict);
```
