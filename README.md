# 联邦子图请求准入

零依赖的 TypeScript 子图准入中间件，用于阻止绕过网关直接访问内网子图的 `_entities` / `_service` 查询。

## 使用

```ts
import {
  createSubgraphAdmissionMiddleware,
  signGatewayRequest,
} from "./index.ts";

const guard = createSubgraphAdmissionMiddleware({
  secret: process.env.GATEWAY_HMAC_SECRET!,
  entities: {
    Product: { keyFields: ["id"] },
  },
  production: process.env.NODE_ENV === "production",
  authorize: async ({ typename, entity, field }) => {
    return policy.allows(typename, entity, field);
  },
});

const result = await guard({
  headers: request.headers,
  body: rawBody, // 必须与签名时使用的原始字节完全一致，不能用重新序列化后的对象
});

if (!result.ok) {
  response.status(result.status).json({
    error: result.code,
    message: result.message,
  });
  return;
}

const entity = await loadProduct(result.entityRequests[0].representation);
const authorized = await result.authorizeEntity("Product", entity, 0);

// authorized.data 使用 GraphQL 响应键（包含别名），只保留获批字段。
// authorized.errors / authorized.denied 记录被逐字段拒绝的路径。
```

网关侧签发请求头：

```ts
const signed = signGatewayRequest(secret, rawBody);
// signed.headers:
// { "x-gateway-signature": "<hex>", "x-gateway-timestamp": "<ms>" }
```

默认规范字符串为 `utf8(rawBody) + utf8(String(timestampMs))`，算法为 HMAC-SHA256。校验端也接受常见的
`timestamp + body`、点号/换行/冒号分隔及 base64 签名格式；所有格式都必须同时绑定原始请求体与时间戳。

## 准入规则

- 所有 GraphQL 请求必须带有效网关签名；时间戳偏差超过 30 秒、签名被篡改或缺失时返回 401。
- `_entities` 的 `representations` 最多 100 个，每个必须是对象，包含已登记的 `__typename`。
- representation 只能包含 `__typename` 和该实体声明的键字段；缺键、额外字段或类型未登记都会拒绝。
- 数组形式的批量请求整体拒绝。
- 生产环境下包含 `__schema` 或 `__type` 的查询返回 403。
- 查询深度按字段嵌套计算，超过 12 层拒绝；fragment 和 inline fragment 计入深度。
- 签名验证使用 `crypto.timingSafeEqual`。响应解析前校验签名，签名失败不会进入 GraphQL 解析。

## 授权

`authorizeEntity` 会在实体解析后再次执行 `authorize` 回调。主回调接收对象参数
`({ typename, entity, field, responseKey, path, selections, context })`；也可使用
`authorizeField` (`(typename, field)`) 或 `authorizeEntity` (`(typename, entity, field)`)。
返回 `true` 或
`{ allowed: true }` 放行；返回 `false`、抛错或未配置回调时仅拒绝当前字段并产生字段错误，不会把整条实体直接放行。

实体配置也可提供 `fields` / `publicFields`，作为类型级字段白名单与回调结果同时生效。

## 测试

```bash
npm test
```

测试覆盖无签名 `_entities`、未登记 `__typename`、多余键字段、过期时间戳、合法网关请求、批量请求、生产自省、超深查询、超量 representations 与逐字段拒绝。
