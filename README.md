# 保存的密码被同源脚本读走

浏览器把保存的密码暴露给同源脚本，攻击者利用注入的第三方脚本读取并外传。请实现供TypeScript页面调用的可导入凭据隔离组件。保存的凭据只在用户手势与受保护上下文可读，第三方脚本与跨源框架无权限；自动填充值不进入DOM可读属性。权限策略与审计。用脚本读取、跨源框架、DOM 检查、合法填充和策略验证凭据不可批量读取。

## 组件用法

```ts
import { CredentialVault, readBrowserContext, inputSink } from './index.ts';

const vault = await CredentialVault.create(); // 可传 { maxRevealsPerGesture, maxRevealsPerWindow, windowMs }
await vault.store('login', secret);

// 读取：调用瞬间实时采集上下文（手势、安全上下文、同源、策略）
const ctx = readBrowserContext(window);
const secret = await vault.reveal('login', { ...ctx, gestureId: String(event.timeStamp) });

// 自动填充：只写 IDL value，不写 content attribute
await vault.autofill('login', inputSink(passwordInput), ctx);

// 审计
await vault.audit.verify();
```

部署侧建议响应头 `Permissions-Policy: credential-isolation=(self)`（见 `RECOMMENDED_PERMISSIONS_POLICY`），第三方 iframe 不授予该特性。

运行测试：`npm test`
