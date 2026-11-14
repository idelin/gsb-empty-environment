# 安全的多租户配置合并

该组件提供无原型、递归冻结的配置合并，并保证每个租户只读取自己的不可变快照。

```ts
import {
  createTenantConfigStore,
  mergeTenantConfig,
} from "./index.ts";

const defaults = {
  authorization: { defaultRole: "user" },
  features: { beta: false },
};

const store = createTenantConfigStore(defaults);
store.setTenantConfig("tenant-a", {
  authorization: { defaultRole: "support" },
});

store.getTenantConfig("tenant-a").authorization.defaultRole; // "support"
store.getTenantConfig("tenant-b").authorization.defaultRole; // "user"

const snapshot = mergeTenantConfig(defaults, {
  features: { beta: true },
});
Object.getPrototypeOf(snapshot); // null
Object.isFrozen(snapshot); // true
```

## 安全约束

- 只读取自有数据属性；继承属性不会参与合并，访问器、Symbol 键和循环引用会被拒绝。
- 键名必须匹配 `^[A-Za-z][A-Za-z0-9_-]{0,63}$`，且禁止 `__proto__`、`constructor`、`prototype` 和包含点号的键。
- 未显式传入 `allowedKeys` 时，只允许覆盖默认配置中已存在的路径；也可传入路径数组、`Set` 或 `(key, path) => boolean`。
- 数组是原子值，租户数组会完整替换默认数组，不按索引递归合并。
- 默认限制为最大深度 8、2,048 个节点和 32 KiB；可通过 `limits` 或顶层选项调整。
- 合并结果使用 `Object.create(null)`，并递归 `Object.freeze`。
- `TenantConfigStore` 在完整构建并冻结快照后才原子替换旧快照；失败补丁不会影响已有租户配置或默认配置。

## 测试

```sh
npm test
```
