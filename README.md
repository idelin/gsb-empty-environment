# Storage Access 手势绑定

可导入的 TypeScript Storage Access 授权组件，模拟浏览器宿主对
`document.requestStorageAccess()` / `document.hasStorageAccess()` 的安全校验，
阻止跨站 iframe 在没有用户手势的情况下获取第一方存储。

## 安全保证

- **瞬时用户激活**：`requestStorageAccess` 必须由嵌入文档上仍在 TTL 窗口内的
  用户手势触发；无激活或激活过期一律 `NotAllowedError` 拒绝，且激活一次性消费。
- **授权键 `(嵌入站点, 顶层站点)`**：授权按二元组隔离，同一嵌入方在不同顶层
  站点下的授权互不影响（`src/grants.ts`）。
- **sandbox 令牌**：sandbox 文档缺少
  `allow-storage-access-by-user-activation` 令牌时以 `SecurityError` 拒绝
  （`src/sandbox.ts`）。
- **顶层换站失效**：顶层跨站导航撤销旧顶层站点下的全部授权，并清空所有瞬时
  激活；同站导航（子域/路径变化）不撤销（`StorageAccessHost.navigateTopLevel`）。
- 同站嵌入与顶层文档默认具备存储访问，无需手势。

## 用法

```ts
import {
  StorageAccessHost,
  ALLOW_STORAGE_ACCESS_BY_USER_ACTIVATION,
} from "./index.ts";

const host = new StorageAccessHost({
  activationTtlMs: 5000,
  frames: [
    { id: "top", origin: "https://publisher.example",
      topLevelOrigin: "https://publisher.example",
      isTopLevel: true, sandboxTokens: null },
    { id: "iframe", origin: "https://ads.attacker.test",
      topLevelOrigin: "https://publisher.example",
      isTopLevel: false, sandboxTokens: null },
  ],
});

// 无手势 → NotAllowedError
await host.requestStorageAccess("iframe");

// 用户点击嵌入文档，刷新瞬时激活；TTL 内请求成功
host.notifyUserGesture("iframe");
await host.requestStorageAccess("iframe");
host.hasStorageAccess("iframe"); // true

// 顶层跨站导航 → 旧授权与激活全部失效
host.navigateTopLevel("https://other-publisher.example");
host.hasStorageAccess("iframe"); // false
```

测试可注入 `clock`（实现 `now(): number`）以确定性地驱动激活过期；
sandbox 场景把 `sandboxTokens` 设为令牌集合，未加 sandbox 时设为 `null`。

## 模块

- `src/activation.ts`：瞬时激活跟踪（TTL、一次性消费、导航清空）
- `src/grants.ts`：按 `(嵌入站点, 顶层站点)` 存储的授权表
- `src/sandbox.ts`：sandbox 令牌校验
- `src/site.ts`：origin → 站点（scheme + eTLD+1）与同站判定
- `src/host.ts`：`StorageAccessHost` 门面，串起全部校验
- `test/storage-access.test.ts`：15 个用例，含激活过期边界测试

## 测试

```sh
npm test
# 可选的严格类型检查（需本地安装 typescript）
npx tsc
```
