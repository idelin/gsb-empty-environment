# 工作区设置安全合并

这是一个无依赖的 TypeScript 设置合并包，用于编辑器宿主在打开仓库时隔离不可信的工作区设置。它可以正确处理解释器路径、终端 shell/profile、格式化程序等可能被当作可执行路径或命令的设置。

## 使用

```ts
import {
  mergeSettings,
  createSettingsMerger,
} from "./index.ts";

const result = mergeSettings(
  { "python.defaultInterpreterPath": "/usr/bin/python3" },
  { "python.defaultInterpreterPath": "./bin/python" },
  { "python.defaultInterpreterPath": "./tools/python" },
  false,
  ["python.defaultInterpreterPath", "terminal.integrated.profiles.*"],
);

console.log(result.settings);
console.log(result.ignored);
```

也可以使用命名参数形式：

```ts
const result = mergeSettings({
  userSettings,
  workspaceSettings,
  folderSettings,
  trusted: false,
  restrictedKeys,
});
```

`mergeSettings` 返回：

- `settings`：合并后的设置对象（别名 `merged`、`mergedSettings`）。
- `ignored`：按层产生的忽略记录数组（别名 `ignoredSettings`）。
- `ignoredKeys`：仅包含被忽略键名的字符串数组。

每条忽略记录包含 `key`、`layer`（`workspace` 或 `folder`）和 `reason`。记录还提供只读别名 `source`、`setting`、`ignoredKey`、`value`。

## 合并规则

- 优先级为 `user < workspace < folder`；受限键在同一层被忽略时，会保留低一层的值。
- 未传 `trusted` 或传 `false` 时按不受信任处理。工作区层和文件夹层的受限键整项不参与合并，并为每个被忽略项生成一条记录。
- 受信任时所有层均按常规覆盖，不产生忽略记录。
- 精确键名区分大小写；以 `.*` 结尾的条目按命名空间前缀匹配，也同样区分大小写。例如 `terminal.integrated.profiles.*` 匹配该命名空间及其子键。
- 键的值是对象或数组时，只要任意深度的对象中出现 `path` 或 `args` 子键，该键就自动视为受限，无需显式列入清单。
- 用户层从不因受限规则被忽略。
- 嵌套对象按键路径递归合并；受限的父级值会被整项跳过。
- 普通对象值按键层覆盖，数组值整体替换。
- 输入对象不会被修改，危险键（`__proto__`、`constructor`、`prototype`）会被跳过。

## 信任状态变化

`mergeSettings` 每次调用都会完整重新计算，不缓存结果。需要长期持有一个合并会话时，可使用 `SettingsMerger` 或 `createSettingsMerger`：

```ts
const merger = createSettingsMerger({
  userSettings,
  workspaceSettings,
  folderSettings,
  trusted: false,
  restrictedKeys,
});

merger.merge();
merger.setTrusted(true);
merger.merge();
```

每次调用 `merge()` 都基于当前 `isTrusted` 状态重新合并，切换信任状态后会得到新结果，不会复用旧结果。

## 测试

```sh
npm test
```

测试覆盖不受信任解释器路径、含深层 `path`/`args` 的终端配置、大小写敏感前缀、文件夹覆盖、用户层保留、输入不变性以及信任切换后的重算。
