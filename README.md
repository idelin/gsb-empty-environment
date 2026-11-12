# 锁定文件别名与OSV区间闭合

扫描器按锁定文件里的包名版本查漏洞，攻击者在 package-lock 里用别名让显示名与真实包不一致，或让 resolved 指向非官方仓库。为TypeScript扫描服务实现供发布门禁调用的锁定文件闭合包：lockfileVersion 3 的 packages 条目取真实名称（name 字段优先，其次路径中最后一个 node_modules 段），resolved 主机不在允许表即报告；对 OSV 的 SEMVER 区间事件（introduced、fixed、last_affected，同一范围按序评估，预发布版本小于对应正式版）判定是否受影响，且每个被锁定的包版本都必须有评估结果，缺失则整体不闭合。用别名包、introduced 为 0、fixed 边界、last_affected 和未评估条目验证。

## 模块结构

- `src/lockfile.ts`：解析 lockfileVersion 3 的 `packages`，提取真实包名并校验 `resolved` 主机。
- `src/semver.ts`：零依赖 SemVer 2.0.0 解析与优先级比较（含预发布规则）。
- `src/osv.ts`：OSV SEMVER 区间事件按序评估。
- `src/scanner.ts`：门禁入口 `scanLockfile`，输出闭合结果。
- `index.ts`：统一导出。

## 门禁语义

- 真实名称：条目 `name` 字段优先（别名安装时 npm 写入真实名），否则取路径最后一个 `node_modules/` 段，支持 scoped 包。
- 主机校验：`resolved` 缺失、无法解析或主机不在允许表（默认 `registry.npmjs.org`，可用 `allowedHosts` 覆盖企业镜像）都会产生 `hostViolations`。
- 区间评估：`introduced`（含 `0`）开启窗口；`fixed` 边界排除（该版本不受影响）；`last_affected` 边界包含（该版本仍受影响）；事件按数组顺序评估，支持多个窗口。
- 闭合（`closed`）：每个锁定包版本都拿到确定评估结果。OSV 数据缺失、区间类型不支持或事件/版本非法都记为未评估，整体不闭合。
- 通过（`passed`）：闭合且无主机违规、无受影响包。闭合不等于无漏洞。

## 用法

```ts
import { parseLockfileText, scanLockfile } from './index.ts';

const lock = parseLockfileText(await fs.readFile('package-lock.json', 'utf8'));
const result = scanLockfile(lock, {
  allowedHosts: ['registry.npmjs.org'],
  advisories: {
    // 键为真实包名；空数组表示已查询且无公告，缺键表示未评估（不闭合）
    'real-vuln-lib': [
      {
        id: 'OSV-EXAMPLE-1',
        packageName: 'real-vuln-lib',
        ranges: [
          { type: 'SEMVER', events: [{ introduced: '0' }, { fixed: '2.0.0' }] },
        ],
      },
    ],
  },
});

if (!result.passed) {
  for (const error of result.errors) console.error(error);
  process.exit(1);
}
```

## 测试

```sh
npm test
```

覆盖别名真实名匹配、`introduced: 0`、`fixed`/`last_affected` 边界、预发布优先级、非官方 `resolved` 主机，以及未评估条目导致的不闭合。
