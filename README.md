# 代码审批检查器（code-approval-checker）

按路径规则计算每个变更文件所需的审批所有者与阈值，结合已有审批（作者本人不计入），
在不超过 20 名候选审批人中求**最少补充审批人集合**；无可行方案时指出阻塞文件。

## 语义定义

- **规则匹配**：`ApprovalRule.pattern` 为 gitignore 风格 glob：
  - 不含 `/` 的模式匹配任意层级目录下的文件 basename（如 `*.ts`）；
  - 含 `/` 的模式锚定仓库根目录（如 `docs/*.md`、`src/**`）；
  - 支持 `*`（不跨 `/`）、`**`（跨目录）、`?`、`[abc]` / `[!abc]`；
  - 前导 `/` 忽略，尾部 `/` 等价于追加 `**`。
- **优先级**：规则按输入数组顺序求值，靠后的规则优先级更高：
  - `override`：用本规则的 `owners` **替换**当前所有者集合；
  - `append`：把本规则的 `owners` **并入**当前所有者集合；
  - 阈值取最后一个声明了 `minApprovals` 的匹配规则，未声明时沿用之前值，
    全局默认由 `defaultMinApprovals` 决定（缺省 1）。
- **逐文件阈值**：每个文件的有效约束为「来自所有者集合的不同审批人数量 ≥ minApprovals」。
- **作者排除**：作者即使在所有者集合或已有审批中，也一律不计数，并从候选中剔除。
- **重命名双路径**：`ChangedFile.oldPath` 存在时，旧路径与新路径的规则**各自**生成一条约束，
  两条都必须满足（同一个所有者的一次审批可同时抵扣两条约束）。
- **无匹配规则**的文件不产生审批要求。

## 输入 / 输出

```ts
checkApprovals(input: CheckInput): CheckResult
```

- `CheckInput`：`author`、`rules`、`files`（含可选 `oldPath`）、`approvals`（已有审批人）、
  `candidates`（可追加邀请的审批人，**至多 20 名**，超出抛错）、可选 `defaultMinApprovals`。
- `CheckResult`：
  - `ok`：是否存在可行方案；
  - `addedApprovers`：最少补充审批人集合（按候选输入顺序输出，结果确定）；
  - `blockingFiles`：不可满足的文件及其缺口（`requirementPath` 指明是旧路径还是新路径的规则阻塞，
    `shortfall` 为即使全部候选都审批仍缺少的审批数）；
  - `files`：逐文件逐路径的明细报告（所有者、阈值、已计入审批、缺口）。

## 求解算法

把每条约束编码为「候选位掩码 + 仍需人数」：约束 `(mask, need)` 被子集 `S` 满足当且仅当
`popcount(mask & S) >= need`。相同掩码的约束合并（取最大 `need`），随后按子集大小递增枚举
（Gosper's hack），第一个满足全部约束的子集即为最优解。复杂度 `O(2^n · m)`，
`n ≤ 20`（约 10^6 个子集），`m` 为去重后的约束数。

## 使用示例

```ts
import { checkApprovals } from './src/index.js';

const result = checkApprovals({
  author: 'carol',
  rules: [
    { pattern: '**', owners: ['alice', 'bob'], mode: 'override', minApprovals: 1 },
    { pattern: 'src/core/**', owners: ['dave', 'erin'], mode: 'override', minApprovals: 2 },
  ],
  files: [{ path: 'src/core/x.ts', oldPath: 'src/core/legacy.ts' }, { path: 'README.md' }],
  approvals: ['alice'],
  candidates: ['bob', 'dave', 'erin'],
});
// result.ok === true, result.addedApprovers === ['dave', 'erin']
```

## 开发与测试

```sh
npm test           # 运行测试（无需任何依赖）
npm run typecheck  # 完整类型检查（需要网络安装 typescript）
npm run build      # 编译到 dist/
```

说明：本仓库源码为标准 TypeScript（`src/*.ts`，ESM，导入使用 `.js` 后缀以兼容 NodeNext）。
当前环境离线、无法安装 `tsc`，因此 `npm test` 通过 `scripts/strip-types.mjs`
对源码做最小类型擦除后在 Node 上直接运行；该脚本只支持 `src/` 中刻意遵守的 TS 子集
（见脚本头部注释）。有网络时请安装 devDependencies 后用 `tsc` 做正式类型检查与构建。
