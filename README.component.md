# 邮件同意授权解释组件

一个零外部依赖、可直接导入 TypeScript 投递宿主的授权解释组件。所有授权输入都是
Ed25519 签名声明，决策结果附带可独立复核的签名依据。

## 领域语义

- 授权（`grant`）把五维绑定在同一份签名声明里：永久主体 `subject`、用途树节点
  `purpose`（含整个子树）、发送商集合 `senders`、地域集合 `regions`、半开有效期
  `[validFrom, validUntil)`。
- 撤回（`revocation`）按维度做**选择性切分**：只切走一个子用途、地域、发送商或时间窗，
  授权的其余部分继续有效；省略某个维度表示该维度上的全部取值。
- 恢复（`recovery`）在旧窗口结束后开启一个**时间不重叠**的新窗口；半开区间允许边界相接，
  实际重叠会被判为畸形并使该链进入待判。
- 更正（`correction`）按语句 id 整体替换原声明，结论只依赖 `issuedAt/seq`，与送达顺序无关。
- 同一签发者在同一 `(chainId, seq)` 上出现不同载荷即分叉，整条链不可用于授权。
- 多个别名可映到同一永久主体并共享其同意；一个别名映到多个主体则该接收者不明确。

## 单一链规则

一次发送的用途、主体、地域、发送商、时间必须由**同一条同意链**同时满足。组件绝不会把
链 A 的用途和链 B 的地域拼成一次发送权；`coveringChains` 返回的每条链都独立覆盖完整查询。
迁移列表时可用 `chainHint` 钉住来源链，钉选无法满足时进入待判而不会悄悄换链。

## 决策与回执

- `evaluateBatch` 输出 `deliverable / pending / denied` 三个子集；`deliverable` 是带签名依据的
  最大可投递子集合。接收者未知/歧义、签名无效、链分叉、边界不明的项一律 `pending`。
- `senderReceipts` 为每个发送商生成隔离回执：发送商只能看到自己负责的项，回执中不含任何
  其他发送商是否持有同意的信息，无法据此推断第三方授权状态。
- `verifyBasisSignatures` 可用原始声明和密钥目录独立复核回执中的每个签名。

## 两个引擎

- `ReferenceAuthorizationEngine`：逐时刻从原始签名输入全量重算，无缓存，作为对照预言机。
- `IncrementalAuthorizationEngine`：按链维护物化缓存，新增声明/选择性撤回只让受影响链失效，
  `cacheStats()` 可观测重算范围；`snapshot()` 与 `fullRebuildReference()` 用于证明缓存与全量一致。

## 入口

- `src/index.ts`：全部公开类型与工具。
- `src/reference-engine.ts`、`src/incremental-engine.ts`：也支持子路径导入。

## 运行测试

```sh
node --experimental-strip-types --test test/*.test.ts
```

测试覆盖：选择性撤回、重叠授权只撤其一、跨链不可拼接、半开时间边界、时间窗打孔、别名归一与
歧义、发送商回执隔离、更正/恢复/分叉、链钉选、篡改签名待判，以及多时刻多乱序下参考引擎与增量
引擎的逐时刻一致性和缓存选择性失效。
