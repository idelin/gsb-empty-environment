# MV3主机权限最小化

攻击者可借过宽的host_permissions和web_accessible_resources读取站点或探测扩展。为TypeScript上架服务实现可导入的清单比对包：实现匹配模式覆盖判定（scheme、*.子域、路径通配、<all_urls>），按声明功能算最小模式集，报告超额项。用*://*.example.com/*覆盖example.com测试。

## 用法

零依赖的 ESM/TypeScript 包，Node 23 可直接通过类型剥离运行：

```ts
import {
  matchesUrl,
  patternCovers,
  minimalPatternSet,
  auditManifest,
} from './index.ts';

// 1. 单 URL 匹配（scheme、*. 子域、路径通配、<all_urls>）
matchesUrl('*://*.example.com/*', 'https://example.com/'); // true，裸域也覆盖

// 2. 模式间覆盖判定（A 覆盖的每个 URL 是否都被 B 覆盖）
patternCovers('*://*.example.com/*', 'https://example.com/api/v2/*'); // true
patternCovers('https://example.com/*', 'https://*.example.com/*');     // false

// 3. 按声明功能计算最小模式集
minimalPatternSet({
  requiredUrls: ['https://cdn.example.com/assets/app.js',
                 'https://cdn.example.com/assets/theme.css'],
});
// => ['https://cdn.example.com/assets/*']

// 4. 清单比对，报告超额/冗余/缺失/非法模式与 WAR 暴露风险
const report = auditManifest(
  { host_permissions: ['<all_urls>'] },
  { requiredUrls: ['https://api.example.com/v2/users'] },
);
// report.excess[0].reasons => ['all-urls']
// report.required           => ['https://api.example.com/v2/users']
```

## 模块

- `src/match-pattern.ts`：`parseMatchPattern`、`matchesUrl`、`patternCovers`、`globCovers`。
  覆盖判定把两个 `*`-glob 编译为 Thompson NFA，用子集积模拟做精确语言包含，
  不靠样本字符串猜测；`*.example.com` 同时匹配裸域 `example.com` 与其子域。
- `src/minimize.ts`：`minimalPatternSet`、`reducePatterns`、`patternFromUrl`，
  合并同主机路径组、剔除被覆盖的模式，输出最小集合。
- `src/audit.ts`：`auditManifest`，读取 `host_permissions`、`content_scripts`、
  `web_accessible_resources`，输出 `excess`（含 `scheme/host/path-too-broad`、
  `all-urls`、`unrequired` 原因与收紧建议）、`redundant`、`missing`、`invalid`
  及 `webResourceRisks`。

## 测试

```sh
npm test
```

包含 43 个用例，覆盖 scheme/子域/路径/`<all_urls>` 语义、`*://*.example.com/*`
覆盖裸域 `example.com` 的验收场景，以及对三字符字母表、长度 8 字符串宇宙的
glob 语言包含穷举校验。
