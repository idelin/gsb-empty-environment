# 账户标识规范化与锁定计数

本包提供可直接被 TypeScript 认证服务导入的账户标识规范化与
generation-aware 锁定组件。锁定与审计只使用规范化后的 key，原始输入单独保留用于
显示。

## 规范化规则

用户名：

- 去除首尾空白和不可见格式字符；
- 执行 Unicode NFKC；
- 执行大小写折叠（包括 `ß -> ss`、词尾 sigma 等）；
- 将全角点、句号等点状字符统一为 `.`，并去掉末尾点，例如
  `ＡＬＩＣＥ．`、`Alice...` 都归一为 `alice`；
- 拒绝控制字符、零宽字符、混用文字系统，以及 Latin/Greek/Cyrillic 等
  常见同形异义字符；非 ASCII 十进制数字同样拒绝。

邮箱：

- 本地部分执行 Unicode NFKC，但保持大小写；
- 本地部分去掉末尾点，并折叠别名：所有域名去掉 `+tag`，Gmail/Googlemail
  额外去掉本地部分中的 `.`；
- 域名执行 Unicode NFKC、大小写折叠、去掉末尾点；
- `googlemail.com` 与 `gmail.com` 在锁定 key 中视为同一域名。

## 可导入 API

```ts
import {
  accountIdentity,
  normalizeUsername,
  normalizeEmail,
  normalizeAccountIdentifier,
  AccountLockoutTracker,
} from "./index.ts";

normalizeUsername("ＡＬＩＣＥ．"); // "alice"
normalizeEmail("victim+news@Example.COM."); // "victim@example.com"

accountIdentity("Alice").key; // "alice"
accountIdentity("Alice").display; // "Alice"（原值单独保存）

const tracker = new AccountLockoutTracker({ threshold: 5 });
tracker.recordFailure("Alice", "password");
tracker.recordFailure("ＡＬＩＣＥ．", "otp"); // 与上一条共享计数
tracker.recordSuccess("ALICE", "sso"); // 成功重试重置失败计数
tracker.isLocked("alice");

const oldGeneration = tracker.getGeneration("alice");
tracker.unlock("Alice"); // 管理员解锁，代次 + 1
tracker.recordFailure({ raw: "Alice", generation: oldGeneration }); // 旧代次请求被忽略
```

如果需要把策略注入服务，可使用
`new AccountIdentifierNormalizer(options)` 或 `createAccountNormalizer(options)`；
也可以把实例作为 `normalizer` 传给 `AccountLockoutTracker`。

`AccountLockoutTracker` 还导出别名 `LockoutTracker`、`LockoutStore`、
`AccountLockoutStore` 和 `createLockoutTracker`。审计记录可通过
`getAuditLog(identifier)` 获取，每条记录同时包含 `key`、`display`、认证方式、
结果和代次。

运行测试：

```sh
npm test
```
