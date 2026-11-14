# 登录凭据填充策略

供 TypeScript 登录门户调用的同步填充策略组件。它在写入任何凭据前校验精确来源、全部框架祖先、表单独占的动作目标，以及可信用户手势和瞬时激活状态；相似域名、跨源嵌入和动作替换都会被拒绝。每次尝试都会生成不可变审计记录，且记录中不包含用户名或口令。

## 使用

```ts
import { createCredentialFillPolicy, captureFrameContext } from "./index.ts";

const policy = createCredentialFillPolicy({
  onAudit: (record) => auditSink.write(record)
});

fillButton.addEventListener("click", (event) => {
  // 保持同步：不要在 fill 前 await，瞬时用户激活会失效。
  const result = policy.fill({
    credential: {
      id: "work-account",
      origin: "https://login.example.com",
      username: "alice@example.com",
      password: savedPassword
    },
    document,
    frame: captureFrameContext(window),
    gesture: event,
    form: "#login-form"
  });

  if (!result.ok) showNotice(result.reason);
});

const records = policy.getAuditLog();
```

`captureFrameContext(window)` 只读取浏览器提供的文档 URL 和 `location.ancestorOrigins`。不要从查询参数、`postMessage` 或页面状态构造框架来源。

## 放行条件

- **精确来源**：文档 URL 的 `origin`（协议、主机、端口）必须与凭据 `origin` 的 `origin` 完全相等。`URL` 解析会规范化默认端口、大小写和 IDN，但不会做后缀或子域匹配；`login.example.com.attacker.test`、Unicode 同形字和端口替换均拒绝。
- **框架祖先**：每个祖先来源都必须与凭据来源完全相等。顶层文档必须使用空的祖先列表。无法读取或无法证明的祖先关系按失败关闭处理；跨源父框架直接拒绝。
- **表单动作**：使用 `document.baseURI` 解析动作，校验普通动作、`formaction` 覆盖（含通过 `form` 属性关联到表单的外部控件）以及可选的精确动作钉 `credential.formAction`。动作协议必须是 HTTP(S)，且来源必须精确相等；`javascript:`、带用户信息的 URL 和相似域名均拒绝。填充前后都会重新校验，动作在写入过程中变化时会清空字段并拒绝。
- **用户手势**：`gesture` 必须是可信事件（真实浏览器中还会校验当前窗口的 `Event` 实例）；`navigator.userActivation.isActive` 存在时必须为 `true`。没有手势、非可信事件或激活已过期均不填充。
- **字段**：自动寻找唯一的当前密码字段，排除 `hidden`、`disabled`、`readonly`、`new-password` 和 `one-time-code`；多个密码字段无法唯一确定时拒绝猜测。用户名按 `autocomplete`、字段类型和名称识别，也可显式传入字段或选择器。

默认拒绝非 HTTPS 的凭据来源，仅允许 `localhost` / 回环地址的 HTTP 开发环境。可通过 `requireSecureOrigin: false` 显式放宽，不建议在生产环境使用。

## 审计

`getAuditLog()` 返回按调用顺序递增的快照。每条记录包含时间、结果与拒绝原因、期望来源、实际文档来源、祖先来源、动作 URL、手势状态和填充字段标志；不包含凭据值。`onAudit` 可接入持久化审计管道，内部日志默认保留最近 1000 条，可用 `maxAuditRecords` 调整。

## 测试

```bash
npm test
```

测试覆盖合法填充、相似域名、跨源框架、动作替换、表单外 `formaction` 覆盖、动作钉、缺少/不可信/过期手势、审计不可变性和回环 HTTP 开发场景。
