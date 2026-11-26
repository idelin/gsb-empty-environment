# 联邦社交收件箱所有权判定组件

零依赖 TypeScript 服务端组件（仅使用 `node:crypto` 的 Ed25519），实现联邦社交对象在
多方分叉转让、过期子编辑授权、乱序投递下的收敛式所有权判定。

## 模块结构

- `types.ts`：领域模型与事件信封类型。
- `canonical.ts`：确定性 JSON 规范化编码与 SHA-256 哈希。
- `signing.ts`：Ed25519 密钥生成、用途标签签名与验证。
- `engine.ts`：核心 `OwnershipEngine`，所有权 DAG、分叉恢复、挂起缓冲与证据裁决。
- `replay.ts`：无缓存全事件重放、乱序排列收敛核对、检查点恢复。
- `test-harness.ts`：测试与引导辅助（密钥钱包、已签名事件工厂）。
- `index.ts`：公共导入入口。

## 核心语义保证

### 身份与隔离

- 对象身份绑定永久 ID 与原始签发域，二者只用于对象隔离，绝不参与任何域名排序裁决。
- 每个对象的链、授权、nonce、别名、编辑完全独立记账，冲突不跨对象扩散。

### 所有权转让

- 所有权是带精确父节点与代次的 DAG；同一父节点的多个子转让即构成分叉。
- 每次正式转让必须旧持有人与新接收者双签名，分别用带用途标签的消息核验。
- 父节点已被恢复关闭、代次不匹配、单侧伪造签名均被拒绝（乱序下先挂起，终结时拒绝）。

### 三路及嵌套分叉

- 支持任意路数并发分叉与分叉子分支的嵌套分叉，活跃末端由 DAG 引用关系动态计算。
- 分叉未决期间不存在唯一持有人，任何局部字段编辑一律 `suspend`。
- 恢复必须精确覆盖当前全部活跃末端，并收集满足法定门槛（默认多数）的末端持有人签名；
   域名排序永远不参与裁决。

### 撤销不可逆

- 公钥撤销、别名退役、委托撤销均以墓碑形式永久留痕，不可复活。
- 恢复合并时，锚定在分叉末端本身的委托被清除；只有锚定在分叉点之前严格祖先上、
   且未被任何分支撤销的授权才能存活。
- 终结阶段对全部委托按签发时刻时间线复核，越权委托统一转为撤销墓碑。

### 子编辑授权

- 委托带 `notBefore/notAfter` 时间窗与字段作用域；到期边界为排他（`atTime >= notAfter` 拒绝）。
- 历史公钥版本永久保留，可核验历史签名；撤销后的密钥不再具备任何新权利。

### 传输签名与重放

- HTTP 投递签名仅产生 `TRANSPORT_SIGNATURE_ADVISORY` 参考证据，不能授权任何编辑。
- 重放的传输 nonce 产生 `TRANSPORT_NONCE_REPLAY` 留痕；权威 nonce 按对象隔离记账。

### 乱序收敛

- 事件依赖未就绪时进入挂起缓冲（确定性 `(timestamp, eventId)` 排序排空），而非立即拒绝。
- `settle()` 显式终结：完整事件集到齐后，不可满足的挂起事件转为永久拒绝。
- 语义哈希只包含权威业务状态（链、授权墓碑、编辑、密钥、别名），与摄取顺序无关；
   `assertConvergent` 可对多轮乱序重放逐对象核对。

### 检查点

- `checkpoint()` 输出语义哈希与可恢复快照；压缩仅影响编辑骨架标志，不改变裁决状态。
- 重放检查点标记事件时强制重新计算哈希，伪造压缩点被 `CHECKPOINT_HASH_MISMATCH` 拒绝。

## 用法

```ts
import { OwnershipEngine, replayAll, assertConvergent, replayPermutations } from './index.ts';

const engine = new OwnershipEngine();
for (const event of federatedEvents) {
  const result = engine.ingest(event);
  // result.status: applied | duplicate | pending | rejected
  // result.evidence: 允许 / 拒绝 / 挂起的可审计证据清单
}
engine.settle(); // 确认不会再有事件到达后终结挂起

// 无缓存全事件重放 + 乱序收敛核对
const reports = replayPermutations(federatedEvents);
assertConvergent(reports);
```
