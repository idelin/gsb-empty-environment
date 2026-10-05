# coverage-migrator

覆盖率迁移工具：当源码演进导致语句**移动、拆分、合并**时，把旧版本的语句覆盖率安全地迁移到新版本。

核心原则：**只有能证明对应关系的语句才继承覆盖**。工具绝不按行号平移来冒充对应关系——行号只用于展示，不参与任何匹配。

## 对应关系的判定依据（仅此两类）

1. **内容指纹唯一匹配**（自动）：语句源码经归一化（去注释、压缩空白）后计算 SHA-256 指纹。同一指纹在旧、新两侧各只出现一次时，可证明是同一语句的移动，自动继承。指纹一对多/多对多时**不猜测**，进入不确定项。
2. **显式映射规则**（人工声明，逐条校验）：
   - `identity`：一对一，要求两侧指纹一致（用于 id 变更、内容未变）；
   - `rename`：一对一，内容虽变但人工确认语义同一（必填 `reason`）；
   - `split`：一对多拆分，各分支继承来源 hits 并标注出处（未经逐分支验证）；
   - `merge`：多对一合并，**来源分别保留**，合并 hits 取各来源最小值（保守）。

规则校验失败（引用不存在的语句、identity 指纹不符、语句被多条规则占用等）会被拒绝并列入不确定项，绝不强行继承。

## 输出

报告包含四类结果（另附 `migratedCoverage` 作为新基线）：

- **继承 inherited**：新语句 + 来源列表 `sources`（多对一时逐条保留）+ 继承方式 `via`；
- **新增 added**：无法证明来源的新语句，覆盖从零开始；
- **不确定 uncertain**：指纹歧义、被拒绝的规则、无对应的旧语句，需人工补充规则后重跑；
- **migratedCoverage**：继承项带来源、新增项 hits=0 的新覆盖基线。

## 使用

要求 Node.js >= 22.6（直接运行 TypeScript）。

```bash
node src/cli.ts --old examples/old-coverage.json \
                --new examples/new-statements.json \
                --rules examples/rules.json \
                --format text
```

退出码：存在不确定项为 `1`，否则 `0`；参数错误为 `2`。

```bash
npm test   # 运行测试
```

## 输入格式

旧覆盖数据（`--old`）：语句 id + 源码区间 + 命中次数 + 内容指纹。

```json
{
  "version": 1,
  "statements": [
    {
      "id": "st-1",
      "range": { "file": "src/app.ts", "start": { "line": 3, "column": 0 }, "end": { "line": 3, "column": 20 } },
      "hits": 120,
      "fingerprint": "..."
    }
  ]
}
```

新语句清单（`--new`）结构相同但无 `hits`。指纹可用 `fingerprintOf(源码文本)` 计算（见 `src/fingerprint.ts`），也可由外部插桩工具提供。

映射规则（`--rules`，可选）：

```json
{
  "version": 1,
  "rules": [
    { "kind": "identity", "oldId": "st-2", "newId": "st-total" },
    { "kind": "rename", "oldId": "s1", "newId": "s2", "reason": "签名扩展，语义同一" },
    { "kind": "split", "oldId": "st-3", "newIds": ["st-3a", "st-3b"], "reason": "复合语句拆分" },
    { "kind": "merge", "oldIds": ["st-4", "st-5"], "newId": "st-45", "reason": "合并为一行" }
  ]
}
```

## 代码结构

- `src/types.ts`：数据模型（语句、区间、文件格式）
- `src/fingerprint.ts`：源码归一化与内容指纹
- `src/rules.ts`：映射规则定义与校验
- `src/migrate.ts`：迁移引擎（规则优先，指纹唯一匹配其次，其余分类为新增/不确定）
- `src/report.ts`：文本报告
- `src/cli.ts`：命令行入口
- `test/migrate.test.ts`：场景测试（移动、拆分、合并、歧义、规则拒绝、禁止行号平移等）
