# 私网访问预检与地址空间

攻击者页面借 DNS 重绑定或 IPv4 映射的 IPv6 地址，让公网页面访问内网设备。为TypeScript浏览器网络栈写可导入的私网访问预检组件：按连接实际对端 IP 而非主机名划分 loopback、private、public，::ffff:a.b.c.d 先还原再判定；请求方比目标更公开须先预检，响应带 Access-Control-Allow-Private-Network: true，缓存键含目标 IP；实际连接 IP 与预检不同则中止。测重绑定、映射地址、缓存键。

## 模块

- `src/ip.ts` — 零依赖的 IP 解析、`::ffff:a.b.c.d` 还原、地址空间判定（`loopback` / `private` / `public`）与公开度比较。
- `src/preflight.ts` — `PrivateNetworkAccessGuard`：预检决策、带目标 IP 的缓存键、连接对端 IP 复核。
- `src/errors.ts` — `PnaError` 及 `InvalidIpError`、`PrivateNetworkAccessDeniedError`、`RebindingDetectedError`。
- `index.ts` — 统一导出入口。

判定只依据连接的实际对端 IP，绝不使用主机名；`::ffff:a.b.c.d` 与遗留的 `::a.b.c.d` 在分类前先还原为内嵌 IPv4。

## 用法

```ts
import {
  PrivateNetworkAccessGuard,
  RebindingDetectedError,
  type PreflightTransport,
} from './index.ts';

// 由浏览器网络栈实现：发出真正的 OPTIONS 预检请求。
const transport: PreflightTransport = {
  async sendPreflight(request, headers) {
    return fetch(request.url, {
      method: 'OPTIONS',
      headers,
    }).then((response) => ({
      status: response.status,
      headers: Object.fromEntries(response.headers),
    }));
  },
};

const guard = new PrivateNetworkAccessGuard({ transport });

async function connect(url: string, targetIp: string, initiator: string) {
  // targetIp 必须是 socket 观察到的对端地址，而非 DNS 主机名。
  const authorization = await guard.authorize({
    url,
    method: 'POST',
    targetIp,
    initiator, // 可传地址空间名或请求方对端 IP
  });

  const socket = await openSocket(url); // 真实连接，拿到实际对端 IP

  try {
    // 预检与真实连接之间若发生 DNS 重绑定，这里抛出并中止。
    guard.verifyConnection(authorization, socket.remoteIp);
  } catch (error) {
    if (error instanceof RebindingDetectedError) {
      socket.destroy();
      throw error;
    }
  }
  return socket;
}
```

## 语义

- 公开度排序 `loopback < private < public`；仅当请求方比目标更公开（如 public → private）时触发预检。
- 预检发送 `Access-Control-Request-Private-Network: true`，要求响应为 2xx 且带 `Access-Control-Allow-Private-Network: true`（大小写、空白容错）。
- 缓存键为 `origin + method + 规范化目标 IP`，因此同源切换到新 IP（重绑定）不会命中旧授权；`Access-Control-Max-Age` 控制有效期并设上限。
- `verifyConnection` 比对规范化后的实际对端 IP 与预检 IP，不一致即抛 `RebindingDetectedError`。

## 测试

```sh
npm test
```

使用 Node 内置 test runner（`node --test`，原生 TS 类型擦除），覆盖 DNS 重绑定、IPv4 映射地址与缓存键三大场景，共 48 个用例。
