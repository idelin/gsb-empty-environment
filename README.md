审批产品会把同一表单草稿同时显示在两个窗口，网络代理还会对超时操作重试。请给 TypeScript 服务端写可导入的动作授权包。签名授权绑定主体、窗口会话、对象版本、规范化表单的字段路径与值、允许的操作含义以及授权级与单子操作级最大消费次数。允许一个确认动作包含多个相互依赖的子操作；子操作 ID 只在其父动作下唯一。权限策略可在提交期间撤销某个字段或收缩动作级限额，异步验证在撤销前开始不代表提交时仍有效。底层事务存储只保证每条写的原子比较交换，可暂停、重复回调和回放已成功但未应答的提交。设计准备记录与最终决定的持久协议，使两个窗口竞争同一授权时至多一个取得实际写权限，动作级消费严格受限，重试能准确区分已执行、可继续和结果未知；在故障恢复时不能仅凭已占用授权推断业务子操作已经完成。为授权快照与每次实际写给可独立回读的消费证明，含必须经过但尚未执行的子操作时不得宣称全成。用逐写点故障和小规模交错模型对照真实实现，覆盖路径别名、子操作重排、动作级限额耗尽、撤销迟到与两个父动作同号子操作。

## 实现

零依赖 TypeScript 包（仅用 `node:crypto` / `node:test`），入口：

- `src/index.ts` — 公开 API：签名/校验、规范化、CAS 存储、策略存储、业务网关、`AuthorizationService`。
- `src/testing/index.ts` — 确定性交错调度器、故障注入存储与测试夹具。
- `docs/protocol.md` — 准备记录/最终决定持久协议与重试语义。

核心保证：

- 单写者指针通过创建式 CAS 竞争，两个窗口竞争同一授权时至多一个获得实际写权限。
- 不可变 claim 票据计数动作级消费；子操作计数器与网关效果共同约束单子操作限额。
- 授权快照与每次业务写都有独立可回读的证明键；存在必须但未执行的子操作时只返回 `PARTIAL`，绝不返回 `SUCCESS`。
- 重试区分 `SUCCESS` / `PARTIAL` / `RETRYABLE` / `UNKNOWN` / `CONFLICT` / `FAILED`；恢复时只认子证明 + 网关效果，不认占用中的授权。

## 用法

```ts
import {
  AuthorizationService,
  InMemoryBusinessGateway,
  InMemoryCasStore,
  InMemoryPolicyStore,
  signGrant,
  canonicalizeForm,
} from "./src/index.ts";

const { digest } = canonicalizeForm(form, aliases);
const grant = signGrant({ /* subject, windowSessionId, objectVersion, formDigest, ... */ }, key);
const service = new AuthorizationService({
  store: new InMemoryCasStore(),
  policy: new InMemoryPolicyStore(),
  gateway: new InMemoryBusinessGateway(),
  signingKeys: [key],
  aliases,
});
const result = await service.submit({ grant, /* form, actionId, idemKey, subOperations */ });
```

## 测试

```sh
npm test
```

覆盖路径别名、子操作重排、动作级与子操作级限额耗尽、撤销迟到、两个父动作同号子操作，以及每个写点的确定性故障注入和双窗口随机交错调度。
