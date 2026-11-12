# 通行密钥条件式中介跨站阻断

第三方广告框架可调用条件式中介，探测访客是否有通行密钥。为TypeScript登录页SDK实现get()发起前的环境闸门，供页面脚本调用：须为可信顶层或同站框架且权限策略允许publickey-credentials-get，allowCredentials为空，同页只留一个条件式请求，新请求先中止旧请求。用跨站iframe、重复发起和导航后晚到响应验证只有合法路径触达浏览器接口。

## 实现

页面脚本通过 `conditionalGet()` 发起条件式中介（passkey autofill）请求，
请求在触达 `navigator.credentials.get()` 之前必须依次通过环境闸门：

1. 安全上下文，且为顶层框架或同站嵌套框架（跨站 iframe、opaque origin 直接拒绝）；
2. Permissions Policy 中 `publickey-credentials-get` 为 `granted`，查询能力缺失时失败关闭；
3. `allowCredentials` 必须为空——非空直接拒绝，空值在出站请求中强制归一为 `[]`；
4. 同页同一时刻只保留一个条件式请求，新请求先 `AbortController.abort()` 旧请求；
5. `pagehide` 导航时中止在途请求，导航后才结算的响应一律丢弃并以 `AbortError` 拒绝。

任一闸门失败都会抛出带 `code` 的 `GateError`，且浏览器接口零调用。

## 用法

```ts
import { conditionalGet, GateError } from './index.ts';

try {
  const credential = await conditionalGet({
    challenge: new Uint8Array([/* ... */]),
    rpId: 'login.example.com',
  });
  // 仅在全部闸门通过且浏览器返回凭据时到达
} catch (error) {
  if (error instanceof GateError) {
    // error.code: cross-site-frame | permission-policy-blocked | ...
  }
}
```

需要自定义环境（测试或非浏览器容器）时，向 `conditionalGet(publicKey, env)`
传入 `GateEnvironment`，或直接 `new ConditionalMediationGate(env)`。

## 结构

- `src/gate.ts` — 闸门主体：框架可信性、权限策略、单飞与导航防护
- `src/sameSite.ts` — scheme + eTLD+1 同站判定（内置常见多部分后缀）
- `src/browserEnv.ts` — 真实浏览器全局到 `GateEnvironment` 的适配层
- `src/types.ts` — 最小 DOM 无关类型与 `GateError`
- `test/` — 跨站 iframe、重复发起、导航晚到响应等 34 个用例

## 测试

```sh
npm test
```
