# SSDP 设备描述获取与控制 URL 校验包

供 TypeScript 媒体控制点的设备发现流程调用。针对局域网内伪造 SSDP
响应（LOCATION 指向内部管理接口、描述中放置指向其他内网主机的绝对
controlURL、借控制点发起 SOAP 请求等）做纵深防御，零第三方依赖。

## 安全边界

- **LOCATION 锚定**：主机必须与 SSDP 数据报来源地址完全相同，且仅接受
  私有地址（IPv4 `10/8`、`172.16/12`、`192.168/16`；IPv6 `fc00::/7`），
  仅允许明文 `http` 与显式/默认 80 端口；DNS 名称、非规范 IP 编码
  （十进制整数、十六/八进制、尾点等会被 URL 规范化的形式）一律拒绝。
- **重定向不换主机**：手动逐跳处理 301/302/303/307/308，相对 Location
  按当前 URL 解析后仍须指向同一主机，换主机的跳转在建立连接前拒绝。
- **URLBase / controlURL 回钉主机**：相对引用按 URLBase 解析，绝对
  controlURL（含协议相对 URL）解析后的主机必须仍等于 SSDP 来源主机。
- **XML 安全**：响应体流式计数、超过上限立即销毁套接字；自带无 DTD
  解析器，拒绝 DOCTYPE/ENTITY，仅支持五个预定义实体与数字字符引用，
  不存在实体表，外部实体（XXE）与实体膨胀均无法触发。
- **SOAPAction 白名单**：只能按设备描述中声明的 `serviceType` URN
  构造请求，SOAPAction 固定为 `"serviceType#action"`，参数做 XML 转义。

## 用法

```ts
import { fetchDeviceDescription, buildSoapRequest } from "./index.ts";

// ssdpSource 是收到 SSDP 应答的套接字远端 IP
const descriptor = await fetchDeviceDescription(
  ssdpLocationHeader,
  ssdpSource,
  { maxXmlBytes: 64 * 1024, maxRedirects: 5, timeoutMs: 5000 },
);

const soap = buildSoapRequest(
  descriptor,
  "urn:schemas-upnp-org:service:AVTransport:1", // 必须在描述中声明
  "Play",
  { InstanceID: 0, Speed: 1 },
);
// soap.url / soap.method / soap.headers.SOAPAction / soap.body
```

所有安全拒绝都抛出 `DescriptionFetchError` 的子类，带稳定的 `code`
（如 `REDIRECT_HOST_MISMATCH`、`CONTROLURL_HOST_MISMATCH`、
`XML_TOO_LARGE`、`UNSAFE_XML`、`SERVICE_NOT_DECLARED`）。

## 模块

- `src/network.ts` — 私有地址判定、IP 字面量与主机规范化
- `src/url-policy.ts` — LOCATION/重定向/URLBase/controlURL 主机锚定
- `src/xml.ts` — 无 DTD、无外部实体的最小 XML 解析器
- `src/http-fetch.ts` — `node:http` 传输，手动重定向与流式大小限制
- `src/descriptor.ts` — 描述获取入口与服务清单校验
- `src/soap.ts` — 仅面向已声明服务的 SOAP 请求构造
- `src/urn.ts` — UPnP URN 白名单校验
- `src/errors.ts` — 带错误码的异常体系

## 测试

```sh
npm test
npm run typecheck   # 需要本地可用 tsc；离线沙箱可跳过
```

覆盖四类要求场景：换主机重定向（含第二跳换主机）、指向其他内网地址的
绝对 controlURL / URLBase、超大 XML（Content-Length 与流式两种路径）、
合法设备完整解析；另含 XXE/DOCTYPE、非规范 IP 编码绕过、SOAPAction
白名单等回归用例。沙箱禁止套接字监听，传输层通过注入式
`HttpFetcher`/请求函数做等价的端到端验证。
