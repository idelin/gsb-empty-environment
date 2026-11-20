# Mail Route Authorization

可导入的 TypeScript 邮件路由授权状态机。入口为 `src/index.ts`，核心 API 是
`RoutingAuthorization` / `createRoutingAuthorization()`。

## 声明与验签

声明至少包含 `domain`、`sequence`、`previousDigest`、`mxRules`、
`notBefore`、`notAfter` 和 `revoked`。序号 0 的 `previousDigest` 必须为
`null`；后续声明必须引用上一声明的摘要。摘要由
`computeDeclarationDigest()` 使用规范化 JSON 计算。

```ts
const authority = createRoutingAuthorization({
  clock: () => Date.now(),
  verifyDomainDeclaration: async (declaration, digest, context) => {
    return hostVerify(declaration, digest, context);
  },
  verifyRecoveryDeclaration: async (declaration, digest, context) => {
    return trustedRootVerify(declaration, digest, context);
  },
});
```

宿主验签回调收到规范化声明、摘要和上下文。路由声明的
`context.authority` 为 `domain`，恢复声明为 `trusted-root`。恢复声明的
`previousDigest` 是恰好两个不同分支头的数组，序号必须晚于两个分支头。

## 分叉与恢复

每个域各自维护节点、分支头和待处理队列。相同序号出现不同摘要时，该域立即
进入分叉状态，进行中的握手和新的握手都会被拒绝。只有受信根验证通过的恢复
声明能解除分叉。恢复后的活动分支独立于旧分支，所以旧分支后来补到的声明不会
回滚恢复结果。

后继先于前驱到达时会进入 pending 队列，前驱被接受后自动按序激活。

## MX 与投递握手

MX 主机按 DNS 标签比较。规则可以是精确主机，也可以是以 `*.` 开头的通配，
通配只覆盖一个额外标签，不覆盖裸域或多层子域。每条规则可以绑定多个证书公钥
摘要；匹配时主机和公钥必须落在同一条规则上。

```ts
const handshake = authority.beginHandshake(domain, mxHost, publicKeyDigest);
if (handshake !== null && authority.finalizeDelivery(handshake)) {
  // 在这里立即执行最终 SMTP 投递。
}
```

握手票据缓存并冻结 `domain`、`declarationDigest`、`mx` 和
`publicKeyDigest`。`finalizeDelivery()` 是同步的原子复核：它要求票据仍绑定
当前声明摘要、声明未过期且未撤销、并且同一条 MX 规则同时授权票据中的主机和
公钥。票据被篡改或声明在握手期间被后继替换，都会拒绝。

## 测试

```sh
npm test
```

测试使用可控时钟和乱序提交，覆盖过期、后继先到、旧分支重放、跨规则拼接以及
握手途中撤销。
