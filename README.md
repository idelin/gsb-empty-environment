# iOS 文件保护类降级静态推导包

应用把敏感文件设成锁屏后不可读的保护类，但**原子写、复制和备份恢复会悄悄把文件换成默认保护类**；
设备在锁定状态下落入攻击者手里时，这些文件就能被读出。本包为 TypeScript 移动应用静态审查服务提供
**零依赖、可导入**的保护类推导能力：回放对路径依次执行的操作序列，推导每个路径的最终保护类，
并对照调用方给的路径规则报告降级违规，指出造成降级的具体那一步。

## 保护类强弱

由强到弱（数值越大越强）：

| 保护类 | 强度 |
| --- | --- |
| `complete` | 3（最强，锁屏即不可读） |
| `completeUnlessOpen` | 2 |
| `completeUntilFirstUserAuthentication` | 1 |
| `none` | 0（最弱） |

## 操作语义

| 操作 | 目标路径最终类 | 溯源 |
| --- | --- | --- |
| `create` | 显式 `protectionClass`，缺省取应用默认类 | 记当前步 |
| `atomicWrite` | 显式 `protectionClass`，缺省取应用默认类；**整体替换，不继承被替换文件原类** | 重置到当前步 |
| `copy` | 副本取**应用默认类**（不继承源类）；其后紧跟的 `setAttributes` 会就地改类 | 记当前步 |
| `move` | **保留源文件原类** | 记 move 步，并保留源路径最初设类步 |
| `setAttributes` | 必须显式 `protectionClass`，就地改类 | 记当前步，move 溯源链保留 |
| `restore` | **一律取应用默认类**，丢弃历史类 | 重置到当前步 |

规则匹配：路径与前缀完全相同，或位于前缀目录内（边界感知，`/a` 不会误命中 `/ab`）；
一条路径落在多条规则下时，取其中**最强**要求。最终类弱于要求即报违规。

## 安装与导入

包以 ESM TypeScript 源码发布、无第三方依赖。Node 22.6+ 可直接用类型剥离运行：

```ts
import { inferProtectionClasses } from 'protection-class-inference';
// 或在本仓库内：
// import { inferProtectionClasses } from './index.ts';
```

## 快速开始

```ts
import { inferProtectionClasses } from './index.ts';

const result = inferProtectionClasses({
  defaultClass: 'completeUntilFirstUserAuthentication',
  rules: [
    { prefix: '/var/mobile/Containers/secret', minClass: 'complete' },
  ],
  operations: [
    { op: 'create', path: '/secret/wallet.dat', protectionClass: 'complete' },
    { op: 'atomicWrite', path: '/secret/wallet.dat' }, // 缺省 -> 默认类，降级
    { op: 'create', path: '/tmp/note.txt' },
    { op: 'copy', from: '/tmp/note.txt', path: '/secret/note.copy' }, // 副本 -> 默认类
    { op: 'restore', path: '/secret/backup.db' }, // 恢复 -> 默认类
    { op: 'move', from: '/tmp/note.txt', path: '/secret/note.moved' }, // 保留弱类
  ],
});

console.log(result.violations);
```

每条违规（`Violation`）包含：

- `path` / `requiredClass` / `actualClass`：违规路径、最强要求类、实际最终类。
- `violatingOperationIndex` / `violatingOperation` / `violatingOperationId`：**造成降级的那一步**。
- `moveSourcePath` / `originOperationIndex` / `originOperationId`：`move` 带入弱类时，溯源到源路径最初设类步。
- `reason`：可直接放入审查报告的中文说明。

`paths` 给出每个仍存在路径的 `finalClass`、`requiredClass`、`compliant` 与 `decidingOperationIndex`。

## 校验的四类降级场景

1. `create complete` 后**缺省 `atomicWrite`**：回落默认类，定位到 `atomicWrite` 步。
2. **`copy`** 进受规则目录：副本为默认类，定位到 `copy` 步；紧跟 `setAttributes` 提升后合规。
3. **`restore`**：一律默认类，定位到 `restore` 步。
4. **`move`** 进受规则目录：保留弱类，定位到 `move` 步并溯源源路径最初设类步。

## 目录结构

```
index.ts               # 包入口，re-export src
src/
  types.ts             # 公共类型与 InferenceError
  classes.ts           # 强弱比较、取最强类
  rules.ts             # 边界感知前缀匹配、多规则取最强
  engine.ts            # 顺序回放、溯源、违规判定
  index.ts             # 桶导出
  engine.test.ts       # node:test 测试（21 个用例）
examples/usage.ts      # 可运行示例
tsconfig.json
```

## 运行测试与示例

使用支持 `--experimental-strip-types` 的 Node（已在 Node v23 验证）：

```bash
node --experimental-strip-types --test "src/**/*.test.ts"
node --experimental-strip-types examples/usage.ts
```

`npm test` 已配置为上述测试命令。输入不合法（如 `copy`/`move` 引用不存在的源路径、
`setAttributes` 缺保护类）时抛出带 `operationIndex` 的 `InferenceError`。
