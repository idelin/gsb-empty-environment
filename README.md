# ActivityPub 收件箱对象归属校验

面向 TypeScript 联邦服务的收件箱授权包。它解决的问题是：收件箱活动里的 `actor`
与内嵌对象可以互不相干，攻击者即便持有自己服务器的合法 HTTP 签名，仍可能在活动里
冒用他人 `actor`，或嵌入他人服务器上的对象。本包在活动进入业务处理前完成归属校验，
校验失败的活动一律忽略。

HTTP 签名校验与对象取回均由调用方注入，本包不绑定任何 HTTP 框架或加密库。

## 授权规则

1. **签名 actor 一致性**：从 HTTP 签名的 `keyId` 去掉 fragment 得到密钥所属 actor，
   它必须与活动的 `actor`（字符串或 `{ id }`）完全相等，否则拒绝（`signer_actor_mismatch`）。
2. **Delete / Update**：仅当目标对象的 `attributedTo` 等于该 actor 时生效。
   内嵌对象直接核对；裸 id 目标取回一次后核对，不符则拒绝（`attribution_mismatch`）。
3. **Create / Announce**：
   - 内嵌对象 `id` 与 actor 同域：信任投递内容，不取回，但仍要求 `attributedTo` 一致；
   - 内嵌对象 `id` 与 actor 跨域，或对象本身是裸 id：丢弃内嵌副本，从对象 `id`
     重新取回权威副本，并要求权威副本 `attributedTo` 等于该 actor，否则忽略。
4. **对象 id 约束**：所有待取回的 `id` 必须是 `https` 且不允许携带 userinfo
   （`https://user@host/...`），否则以 `invalid_object_id` 拒绝，且不会发起请求。
5. **取回约束**：每次授权最多取回一次，绝不重试；重定向不跟随——跨域重定向以
   `cross_origin_redirect` 拒绝，同域重定向以 `fetch_failed` 拒绝。

授权通过时返回的 `activity` 已用权威副本替换掉不可信的外域内嵌对象，调用方处理返回值即可。

## 目录结构

- `src/types.ts`：数据模型与授权结果类型。
- `src/url.ts`：https/userinfo 校验、keyId→actor 推导、归属 id 收集。
- `src/authorize.ts`：授权内核 `authorizeInboxActivity`。
- `src/inbox.ts`：收件箱 POST 装配器 `createInboxAuthorizer`。
- `test/`：`node --test` 测试，覆盖冒用 actor、删除他人对象、外域嵌入、取回不一致等场景。

## 用法

```ts
import { createInboxAuthorizer } from './index.ts';

// 调用方实现 HTTP 签名校验（例如 http-message-signatures 库）。
const verifySignature = async (request) => {
  const keyId = await verifyHttpSignature(request); // 失败时抛错
  return { keyId };
};

// 调用方实现对象取回：manual 模式，不自行跟随重定向。
const fetchObject = async (objectId) => {
  const response = await fetch(objectId, {
    headers: { accept: 'application/activity+json' },
    redirect: 'manual',
  });
  if (response.status >= 300 && response.status < 400) {
    return { status: response.status, location: response.headers.get('location') ?? undefined };
  }
  return { status: response.status, body: await response.json() };
};

const authorize = createInboxAuthorizer({ verifySignature, fetchObject });

async function handleInboxPost(request, activity) {
  const result = await authorize(request, activity);
  if (!result.allowed) {
    console.warn('drop inbox activity:', result.reason, result.detail);
    return; // 忽略该活动
  }
  await dispatch(result.activity); // result.activity 已替换为可信内容
}
```

也可以直接调用内核，自行先完成签名校验：

```ts
import { authorizeInboxActivity } from './index.ts';

const result = await authorizeInboxActivity(activity, {
  signature: { keyId: 'https://alice.example/actors/alice#main-key' },
  fetchObject,
});
```

## 判定结果

`AuthorizationResult.allowed === false` 时的 `reason`：

| reason | 含义 |
| --- | --- |
| `signature_rejected` | 注入的签名校验抛错（仅装配器） |
| `signer_actor_mismatch` | keyId 所属 actor 与活动 actor 不一致 |
| `invalid_activity_actor` | 活动 actor 缺失或 id 非法 |
| `invalid_object_id` | 对象 id 非 https、含 userinfo 或不可解析 |
| `attribution_mismatch` | 对象 `attributedTo` 不等于签名 actor |
| `cross_origin_redirect` | 取回被重定向到其他域名 |
| `fetch_failed` | 取回失败、非 2xx 状态或同域重定向（不跟随） |
| `unsupported_response` | 取回内容不是 JSON 对象 |
| `object_not_found` | 活动缺少可解析的对象 |
| `missing_activity_type` | 活动缺少字符串类型的 `type` |

## 测试

```sh
npm test
```

使用 Node 内置测试运行器与原生 TypeScript 类型擦除，无需第三方依赖。
