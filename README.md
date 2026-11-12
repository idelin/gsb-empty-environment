# WebUSB 与 WebHID 设备选择过滤

攻击者页面可用宽松过滤器（如空 `filters` 或空过滤器对象 `{}`）诱使用户授权
键盘、安全密钥等敏感设备。本包为 TypeScript 浏览器宿主提供可导入的设备选择
过滤层：对 `filters` / `exclusionFilters` 做结构校验、组合匹配与受保护设备拦截。

## 安全规则

- **空过滤器拒绝**：`filters` 不能为空数组或 `undefined`；每个过滤器对象必须至少
  包含 `vendorId`、`productId`、`classCode`、`usagePage` 之一，空对象 `{}` 抛
  `TypeError`。`exclusionFilters` 可省略或为空数组，但其中的过滤器对象同样不得为空。
- **字段组合**：单个过滤器内字段为“与”，多个过滤器之间为“或”；字段值必须是
  位宽内的非负整数（`vendorId`/`productId`/`usagePage` ≤ `0xFFFF`，`classCode` ≤ `0xFF`）。
- **受保护设备永不列出**：
  - 受保护用法页 `PROTECTED_USAGE_PAGES`：FIDO `0xF1D0`、键盘/小键盘 `0x0007`
    （同时检查设备顶层与任一 `collections` 的用法页）；
  - 受保护类代码 `PROTECTED_CLASS_CODES`：大容量存储 `0x08`
    （同时检查设备顶层与任一 `interfaces` 的类代码）。
- `buildSafeRequestOptions` 额外拦截**显式**指向受保护用法页/类代码的过滤器，
  防止页面把敏感条件直接透传给 `navigator.hid.requestDevice`。

## 用法

```ts
import {
  selectDevices,
  buildSafeRequestOptions,
} from './index.ts';

// 从候选设备中选择：宽 vendorId 过滤，并排除特定 productId。
const picked = selectDevices(devices, {
  filters: [{ vendorId: 0x1234 }],
  exclusionFilters: [{ productId: 0x0002 }],
});

// 构造传给浏览器原生 requestDevice 的安全参数（含深拷贝）。
const options = buildSafeRequestOptions({
  filters: [{ vendorId: 0x1234, classCode: 0x03 }],
});
```

## API

- `selectDevices(devices, options)`：返回通过校验、匹配包含过滤器、未命中排除
  过滤器且非受保护的设备，保持输入顺序。
- `buildSafeRequestOptions(options)`：校验并返回可透传给原生 API 的
  `{ filters, exclusionFilters }`，命中受保护条件时抛错。
- `isProtectedDevice(device)` / `isProtectedFilter(filter)`：保护判断原语。
- `matchesFilter` / `matchesAnyFilter`：纯匹配逻辑。
- `validateFilter` / `validateFilterList`：结构与取值校验。

## 结构

- `src/constants.ts`：受保护用法页/类代码与字段位宽常量。
- `src/types.ts`：`DeviceFilter`、`DeviceLike` 等类型。
- `src/validate.ts`：空过滤器与字段取值校验。
- `src/protected.ts`：受保护设备/过滤器判断。
- `src/match.ts`：过滤器组合匹配。
- `src/select.ts`：`selectDevices` 与 `buildSafeRequestOptions`。
- `test/`：宽过滤、排除过滤、空过滤器拒绝、受保护设备拦截测试。

## 测试

```sh
npm test          # node --experimental-strip-types --test
node --experimental-strip-types example.ts
```
