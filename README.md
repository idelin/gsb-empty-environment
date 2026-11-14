# OAuth 回调地址校验与授权码绑定

该组件修复了回调地址按字符串前缀匹配导致的开放重定向问题。注册和授权请求都会按完整 URI 解析，逐项比较 scheme、IDNA 规范化后的主机、有效端口、路径段和查询串。

```ts
import {
  createOAuthCallbackValidator,
  createPkceChallenge,
} from "./index.ts";

const validator = createOAuthCallbackValidator();

validator.registerClient({
  clientId: "web-client",
  redirectUris: ["https://app.example.com/oauth/callback"],
});

const verifier = "a-very-long-random-pkce-code-verifier";
const issued = validator.authorize({
  clientId: "web-client",
  redirectUri: "https://app.example.com/oauth/callback",
  codeChallenge: createPkceChallenge(verifier),
  nonce: "request-nonce",
});

// 只把授权码发送到 issued.redirectUri，绝不能回退到请求中的原始字符串。
// 随后在 token endpoint 重新校验 client、redirect_uri、PKCE 和 nonce。
const grant = validator.exchange({
  code: issued.code,
  clientId: "web-client",
  redirectUri: "https://app.example.com/oauth/callback",
  codeVerifier: verifier,
  nonce: "request-nonce",
});
```

## 校验规则

- 仅接受绝对 URI；不允许用户信息、片段、反斜杠或未声明的非默认端口。
- 主机先经 IDNA/UTS-46 规范化为小写 ASCII 域名，再进行完整相等比较。`app.example.com.attacker.io`、子域和 IDN 同形异义域名均不会匹配。
- 默认端口按有效端口比较：`https://app.example.com:443/cb` 与 `https://app.example.com/cb` 等价，`https://app.example.com:8443/cb` 不等价。
- 路径逐段、区分大小写比较。查询串也必须完全一致。
- 通配符只能写成注册路径中的完整 `*` 段，例如 `/oauth/*/callback`；它只匹配一个非空段，不能跨越 `/`。路径中的 `.`、`..`、`%2e%2e`、编码斜杠和反斜杠都会被拒绝。
- 环回地址（`localhost`、`127.0.0.0/8`、`::1`）必须通过 `allowLoopback: true` 单独授权。
- 自定义 scheme 必须通过 `allowCustomSchemes: ["com.example.app"]` 单独授权；`javascript:`、`data:`、`file:`、`vbscript:` 永远拒绝。

```ts
validator.registerClient({
  clientId: "native-app",
  redirectUris: [
    "http://127.0.0.1:3000/callback",
    "com.example.app:/oauth2redirect",
  ],
  allowLoopback: true,
  allowCustomSchemes: ["com.example.app"],
});
```

授权码记录绑定 `clientId`、规范化后的 `redirectUri`、PKCE `codeChallenge`/方法以及可选 `nonce`。兑换时会重新按当前注册信息校验请求中的 `redirect_uri`，并检查它是否与授权码记录完全一致；授权码一次性使用且会过期。注册信息发生变化后，不再匹配的旧授权码无法兑换。

可直接导入的纯函数包括 `parseRedirectUri`、`normalizeRedirectUri`、`validateRedirectUri`、`matchRedirectUri`、`assertRedirectUri` 和 `checkRedirectUri`。

运行测试：

```sh
npm test
```
