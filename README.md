# 跨来源嵌入许可组合（COEP/CORP 准入包）

文档启用跨源隔离（COEP）后，子资源必须**同时**满足资源自身许可（CORP/CORS）
与文档嵌入策略要求。本包是供 TypeScript 浏览器宿主调用的纯逻辑准入库：

- 输入：结构化来源、资源模式、凭据模式、嵌入策略、资源声明；
- 策略由**调用方提供的策略矩阵**决定，矩阵对全部有限属性组合逐一枚举
  `allow` / `deny`（包内附带一份符合 Web 平台 COEP+CORP+CORS 语义的默认矩阵）；
- 重定向链**每一跳重新计算**来源关系并重新走矩阵判定；
- 缓存条目绑定**完整安全上下文**，上下文任何一个维度不同都不会命中同一缓存；
- `test/exhaustive.test.ts` 对全部有限组合穷举，证明同一资源不会因缓存绕过隔离。

## 有限属性空间

| 维度 | 取值 | 数量 |
| --- | --- | --- |
| 来源关系 `relationship` | `same-origin` / `same-site` / `cross-site` | 3 |
| 资源模式 `resourceMode` | `navigate` / `cors` / `no-cors` | 3 |
| 凭据模式 `credentials` | `include` / `same-origin` / `omit` | 3 |
| 嵌入策略 `embedding` | `unsafe-none` / `require-corp` / `credentialless` | 3 |
| 资源许可 `corp` | `none` / `same-origin` / `same-site` / `cross-origin` | 4 |
| CORS 许可 `corsAccess` | `true` / `false` | 2 |

策略空间大小固定为 `3 × 3 × 3 × 3 × 4 × 2 = 648` 个组合。
`createDefaultMatrix()` 生成覆盖全部 648 个键的矩阵，`validateMatrix()`
校验宿主自定义矩阵既不缺键也不含未知键。

## 模块结构

- `src/types.ts` — 全部有限属性联合类型与输入/输出结构；
- `src/origin.ts` — URL 解析为结构化来源，并派生同源/同站/跨站关系；
- `src/matrix.ts` — 键序列化、全组合枚举、默认矩阵、矩阵完备性校验、查表；
- `src/admission.ts` — `AdmissionGate`：单跳准入与重定向链逐跳准入；
- `src/cache.ts` — `SecurityContextCache`：缓存键绑定完整安全上下文；
- `src/host.ts` — `HostAdmission`：宿主门面，一次调用完成准入与缓存写入；
- `index.ts` — 包出口。

## 使用方式

```ts
import { HostAdmission, createDefaultMatrix, parseOrigin } from "./index.ts";

const host = new HostAdmission(createDefaultMatrix());
const documentOrigin = parseOrigin("https://app.example.com")!;

const result = host.admit({
  documentOrigin,
  resourceUrl: "https://cdn.third-party.org/img.png",
  resourceMode: "no-cors",
  credentials: "omit",
  embedding: "require-corp",
  declaration: { corp: "none", corsAccess: false },
});
// result.allowed === false：隔离文档拒绝无 CORP 的跨源 no-cors 响应
```

重定向链（每跳独立评估，任一跳拒绝即整体拒绝，全部获许后只缓存末跳）：

```ts
host.admitRedirectChain({
  documentOrigin,
  resourceMode: "no-cors",
  credentials: "omit",
  embedding: "require-corp",
  hops: [
    { resourceUrl: "https://cdn.example.com/redirect",
      declaration: { corp: "same-site", corsAccess: false } },
    { resourceUrl: "https://cdn.third-party.org/final",
      declaration: { corp: "cross-origin", corsAccess: false } },
  ],
});
```

## 缓存隔离的穷举证明

缓存键由资源 URL、文档来源（scheme/host/port）与完整策略键组成，
即 `buildCacheKey = { resourceUrl, documentOrigin, serializeKey(policyKey) }`。
`test/exhaustive.test.ts` 给出三层证明：

1. **全组合判定**：枚举 648 个组合，`HostAdmission.admit` 的结果与矩阵逐键一致，
   且同一上下文重复访问结果确定、第二次才命中缓存；
2. **键唯一性**：同一资源路径在 648 个上下文下生成 648 个互不相同的缓存键，
   并逐位翻转六个策略维度验证每个维度都参与键的区分，缓存条目数等于获许组合数；
3. **拒绝不可复用**：所有 `allow` 上下文先写缓存后，全部 `deny` 上下文
   （含同一 URL 仅安全上下文不同的情形）缓存查找一律落空；被拒访问不写入缓存，
   随机化访问顺序后结论不变。

因此，某资源在宽松上下文（如 `unsafe-none`）下获许缓存，不会被隔离上下文
（如 `require-corp` 且资源无 CORP）复用——二者安全上下文不同，缓存键必然不同。

## 运行

无第三方依赖，需要 Node ≥ 22.6（内置 TypeScript type-stripping 与 `node:test`）：

```sh
node --test test/*.test.ts
node examples/basic.ts
```
