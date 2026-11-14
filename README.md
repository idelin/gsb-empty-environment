# 子资源完整性校验组件

可直接被前端构建脚本和浏览器运行时导入的 TypeScript SRI 校验组件。它不会
“找到第一个能匹配的摘要就放行”，而是只校验声明中最强的算法。

## 安全规则

- 解析 `integrity` 中的全部 token，并按 `sha512`、`sha384`、`sha256`、`sha1`
  的强度排序。
- 只使用最强算法校验内容；SHA-1 仅用于诊断，不能作为回退，单独出现时直接拒绝。
- 同一最强算法可声明多个摘要，任一摘要匹配即可，但较弱算法的匹配永远不会
  覆盖最强算法的失败。
- 同源和跨源资源走同一条校验路径；加载器只返回已经校验的内容，缺少
  `integrity` 时在读取网络前拒绝，脚本、样式、HTML、WASM 等高风险资源同样
  不会降级放行。
- `fetchVerifiedResource` 校验重定向后的最终响应体，不信任重定向前的 URL 或
  响应正文。
- 缓存值以实际内容摘要为键，URL 仅作为别名；每次缓存命中仍会重新核验当前
  `integrity`。

## API

```ts
import {
  ContentAddressedCache,
  fetchVerifiedResource,
  generateIntegrity,
  parseIntegrity,
  verifyIntegrity,
} from './index.ts';

const content = await generateIntegrity(sourceBytes, ['sha384', 'sha256']);
const parsed = parseIntegrity(content);
const result = await verifyIntegrity(sourceBytes, content);

const cache = new ContentAddressedCache();
const resource = await fetchVerifiedResource('/app.js', {
  integrity: content,
  cache,
  highRisk: true,
});
console.log(resource.text(), result.algorithm, resource.cacheHit);
```

构建脚本应使用 `generateIntegrity` 生成唯一或强算法优先的摘要。运行时使用
`verifyIntegrity` 校验内存中的字节，或使用 `fetchVerifiedResource` 获取经过
最终内容校验的响应。`IntegrityError.code` 可用于区分缺少属性、弱算法、摘要
冲突和网络响应错误。构建时若同时请求 SHA-1 和强算法，SHA-1 会被丢弃；只有
弱算法可用时构建直接失败。

## 测试

```sh
npm test
```

测试覆盖 SHA-1 回退、强弱摘要冲突、重定向替换、缺失属性、合法强哈希
（同源与跨源）以及内容寻址缓存命中。
