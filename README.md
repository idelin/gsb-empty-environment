# Tauri capability 路径作用域

可嵌入桌面 IPC 宿主的 TypeScript capability 判定组件。它不读取文件系统，只负责把
`windowLabel + permission + origin + paths` 映射为 allow/deny 决策。

## 用法

```ts
import { CapabilityEngine } from './index.ts';

const engine = new CapabilityEngine(
  [
    {
      identifier: 'main-capability',
      windows: ['main', 'settings-*'],
      remote: { urls: ['https://*.trusted.example'] },
      permissions: [
        {
          identifier: 'fs:allow-read-text-file',
          allow: ['$APPDATA/**'],
          deny: ['$APPDATA/secret/**', '$HOME/.ssh/**'],
        },
      ],
    },
  ],
  {
    env: { HOME: '/home/alice', APPDATA: '/home/alice/.config' },
  },
);

const decision = engine.check({
  windowLabel: 'main',
  permission: 'fs:allow-read-text-file',
  origin: 'tauri://localhost',
  path: '$APPDATA/notes/../notes.txt',
});

if (!decision.allowed) {
  // decision.reason: 'no-matching-capability' | 'remote-origin-denied' | ...
  throw new Error(decision.message);
}
```

## 判定规则

- `windows` 按标签 glob 匹配，`*` 可匹配 `main`、`settings-2` 以及运行时动态创建的标签；
  没有任何 capability 匹配的标签一律无权限。
- `permissions` 可写成字符串，或带 `allow` / `deny` 路径作用域的对象。显式空
  `allow: []` 表示全部拒绝；省略 `allow` 表示该权限不受路径限制。没有传入路径时，
  带作用域的权限会以 `path-required` 拒绝。
- 所有匹配 capability 的 `deny` 先于 `allow` 求值；任一 `deny` 命中立即拒绝。
- 路径先展开 `$APPDATA`、`$HOME`、`${APPDATA}`、`%APPDATA%`，再统一分隔符、折叠
  重复斜杠并解析 `.` / `..`。相对路径和未知变量 fail closed。
- `*` 只匹配单个路径段，`**` 可跨任意层级目录。Windows 盘符根与大小写不敏感匹配。
- 远程来源（`http(s)` 等）必须命中该 capability 的 `remote.urls`；`remote` 未配置时
  仅本地页面可用。`tauri://localhost`、`http(s)://tauri.localhost`、`file://` 及
  回环地址视为本地页面。省略 `origin` 视为宿主本地调用。

路径匹配是词法匹配；若宿主需要抵御符号链接逃逸，应在调用本组件前对真实路径执行
`realpath`。

## 验证

```sh
npm test
```

测试覆盖 `..` 穿越、重复斜杠伪装、deny 覆盖、`*` / `**` 边界、远程来源与
`https://trusted.example.attacker.example` 前缀绕过、本地页面例外、未知动态标签、
未知变量以及 Windows 大小写。
