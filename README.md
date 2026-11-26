# MV3 权限求解与跨世界消息门禁

离线 TypeScript 包，用于审核 Manifest V3 企业浏览器扩展。它把扩展清单、内容脚本转发、消息处理器路由表和敏感后台能力转换为可执行的权限模型，并搜索网页主世界到特权 Service Worker 的混淆代理人路径。

无第三方依赖、无网络访问。测试使用 Node 内置 TypeScript stripping：

```bash
npm test
```

## 快速导入

```ts
import {
  CapabilityAuthority,
  RuntimeMessageGate,
  analyzeSecurity,
  solveManifestPermissions,
} from "./src/index.ts";
```

包入口为 `src/index.ts`，`package.json` 已声明 `exports`，可直接从包名导入 TypeScript 源文件。

## 清单权限求解

```ts
const solution = solveManifestPermissions(manifestJson, {
  grantedOptionalPermissions: ["scripting"],
  grantedOptionalHostPermissions: ["https://bank.example/*"],
});

solution.isHostPermitted("https://bank.example/account");
solution.contentScriptsForUrl("https://app.example.com/page");
solution.isExternallyConnectable("https://partner.example");
solution.isWebAccessibleForOrigin("options.html", "https://partner.example");
solution.accessProfile.cookies; // 已授予的 API 权限画像
```

支持：

- `<all_urls>` 与 Chrome 允许方案集：`http`、`https`、`file`、`ftp`、`ws`、`wss`、`urn`。
- `*` 方案只匹配 `http`/`https`；主机 `*`、`*.example.com`（含 apex）与精确主机。
- 路径 `*` 通配；路径匹配遵循 Chrome `URLPattern`，只匹配 URL path，忽略 query/fragment。
- 内容脚本 `matches` 与 `exclude_matches` 差集，并支持 `include_globs`/`exclude_globs`。
- `host_permissions`、`optional_host_permissions` 与用户已授予可选权限。
- MV3 `web_accessible_resources` 的 web/extension 两类曝光范围。
- `externally_connectable` 的 web origin 与 extension id。
- `declarative_net_request.rule_resources` 清单校验。

清单验证会拒绝 MV2、把 host pattern 放在 `permissions`、空 WAR 范围、非法匹配模式等问题。

## DNR 优先级

```ts
import { evaluateDeclarativeNetRequest } from "./src/index.ts";

const decision = evaluateDeclarativeNetRequest(rules, {
  url: "https://bank.example/account",
  initiator: "https://attacker.example/page",
  type: "main_frame",
  method: "GET",
});
```

裁决顺序：先比较数值 `priority`；同优先级时按
`allowAllRequests > allow > block > redirect > upgradeScheme > modifyHeaders`，
最后按较小的 `rule.id` 稳定排序。`allowAllRequests` 仅作用于 `main_frame`/`sub_frame`。

条件支持 `urlFilter`（`||`、`|`、`*`、`^`）、`regexFilter`、请求/发起方域名排除、资源类型、方法、tab id 和 `domainType`。`modifyHeaders` 规则即使未赢得最终裁决也会单独返回，供调用方应用。

## 运行时消息门禁

```ts
const authority = new CapabilityAuthority({ secret });
const gate = new RuntimeMessageGate({ manifest, routes, authority });

const token = await gate.issueToken("read-cookie", {
  origin: "https://app.example.com",
  tabId: 7,
  tabPermissions: ["https://app.example.com/*"],
  target: "https://app.example.com/account",
});

const decision = await gate.authorize({
  routeId: "read-cookie",
  messageType: "cookies.read",
  sender: trustedSenderFromRuntime, // sender.origin 来自浏览器运行时，不来自页面 payload
  token,
});
```

`RuntimeMessageGate` 强制：

- `sender.origin` 必须存在且不能是 `null`；payload 中的 `origin` 不能覆盖它。
- route 必须声明可信 origin 校验；`none`/`presence-only` 被拒绝。
- 内容脚本到 Service Worker 必须使用 `senderBinding: "verified"`，`payload-origin` 被拒绝。
- 特权动作必须绑定 `tabId`、实时 `tabPermissions` 与动作级 HMAC-SHA256 能力令牌。
- 令牌必须绑定 route、action、origin、tabId、permission set 和目标；静态共享密钥被拒绝。
- Cookie/脚本注入/DNR 绕过/抓取代理目标必须位于发送方同源边界内；`sender.origin` 之外的 `host_permissions` 不能借后台代理访问。

能力令牌由 WebCrypto `HMAC` 签发，包含过期时间、随机 nonce 和不可变 claims。页面内容永远不应获得签发能力；令牌只应由扩展的可信代码为当前 tab 签发。

## 有界攻击路径搜索

```ts
const result = analyzeSecurity({
  manifest,
  routes,
  entries: [{ pageOrigin: "https://attacker.example", via: "content-script" }],
  maxHops: 4,
});

result.attackPaths[0]?.hops;
result.safe;
```

搜索器支持 `1 | 2 | 3 | 4` 跳，超过上限抛 `RangeError`。每条 `MessageRoute` 是消息处理器图中的有向边；可用 `fromHandler`/`toHandler` 精确串联处理器，未提供时回退到世界级图。边上的 origin allowlist、sender 绑定、令牌绑定、tab 授权和目标作用域均参与可达性判断。

敏感 sink：

- `cookie-read`：跨源读取受保护域 Cookie。
- `script-injection`：向目标域注入脚本。
- `dnr-bypass`：绕过企业阻断规则。
- `fetch-proxy`：借特权后台执行跨域抓取。

路径按跳数排序，`bestPath` 是最小攻击路径；`paths` 保留上限内的其他可证明路径。相同源目标不算跨站混淆代理人 sink。清单未授予 `cookies`/`scripting`/`declarativeNetRequest` 或对应 host 权限时，不会报告虚假 sink。

## 场景矩阵

规范与安全场景在 `tests/` 中以断言矩阵固定：

| 场景 | 期望 | 覆盖位置 |
| --- | --- | --- |
| `<all_urls>` 覆盖 7 种允许方案、排除 data/blob/chrome-extension | 无漏报/无误报 | `tests/match-pattern.test.ts` |
| `*.example.com` 命中 apex/子域，拒绝后缀混淆 | 精确边界 | `tests/match-pattern.test.ts` |
| 内容脚本 include 减 exclude | 差集正确 | `tests/manifest.test.ts` |
| 可选 host 未授予/已授予 | 权限随用户授权变化 | `tests/manifest.test.ts` |
| WAR 暴露 options/popup | 点击劫持 finding | `tests/manifest.test.ts` |
| WAR 仅 extension_ids | 无 web 曝光 | `tests/manifest.test.ts` |
| 同优先级 DNR 五种动作 | `allowAllRequests > allow > block > redirect > modifyHeaders` | `tests/dnr.test.ts` |
| 缺失 origin、伪造 payload origin、缺失 sender 绑定 | 拒绝 | `tests/runtime-gate.test.ts` |
| 缺失/静态/篡改/过期/错误 tab/错误权限集令牌 | 拒绝 | `tests/runtime-gate.test.ts` |
| 跨源 Cookie/注入目标即使有 host 权限与 tab grant | 拒绝 | `tests/runtime-gate.test.ts` |
| 2–4 跳混淆代理人路径 | 找到最小路径 | `tests/attack-path.test.ts` |
| 5 跳链（上限 4） | 不报告 | `tests/attack-path.test.ts` |
| 可信 origin allowlist、完整令牌、同源目标、清单缺权限 | 不报告 | `tests/attack-path.test.ts` |
| WAR 可达特权扩展页、external connectable 直达 worker | 可达入口 | `tests/attack-path.test.ts` |

## 规范说明与边界

- Chrome 匹配模式的 scheme/host/path 语义按官方 `URLPattern` 建模；端口不写入 pattern，主机匹配忽略端口。
- `<all_urls>` 不等同于 `*://*/*`：前者包含 `file`/`ftp`/`ws`/`wss`/`urn`。
- DNR `allowAllRequests` 的跨请求 frame 状态只在本包内按单请求裁决建模；企业审核若需要完整 frame 生命周期，应在宿主层补充 frame 状态。
- WAR 点击劫持检测基于清单声明的 options/popup/devtools/side panel/overrides 页面，以及明确的 `*.html` 规则匹配。`public/`、`web_accessible/`、`assets/` 下的 HTML 不按内部特权页处理。
- 攻击图以消息路由表为输入；动态代码生成、压缩后的 handler 名称和运行时注册的未知路由需在宿主侧提取后注入。
- 全部逻辑离线运行，不读取网络、不访问远程规则或远程清单。
