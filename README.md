# module-conflict-detector

资源打包工具的模块命名冲突检测与最小代价改名建议（TypeScript 实现，无运行时依赖）。

## 路径等价规则（题内规则，纯词法计算）

所有路径等价判断均为**纯字符串运算**，不访问宿主文件系统，因此与平台
（大小写敏感性、路径分隔符）无关：

1. **规范化**：`\` 统一为 `/`，折叠重复斜杠、`.` 与 `..`，去末尾斜杠；
   相对路径越出根时保留多余的 `..`。
2. **别名展开**：`aliases` 支持末尾 `/*` 通配（如 `"@app/*": "src/*"`），
   最长前缀优先，最多展开一次。
3. **扩展名省略**：反复剥掉 `extensions` 中的已知扩展名（大小写不敏感），
   缺省为 `.ts .tsx .js .jsx .mjs .cjs .json`。
4. **大小写折叠**：Unicode 小写化。

模块身份键 = 规范化 → 别名展开 → 扩展名省略 → 大小写折叠。
键相同而实际路径不唯一的模块构成**冲突组**。

## 最小代价改名

- 改名代价由输入 `renameCost` 给出（缺省 1）。
- 各冲突组相互独立：每组保留代价最高者（并列保留输入靠前者），其余改名，
  总代价最小。
- 新名字为 `<基名>-<n>.<扩展名>`（保留原大小写与扩展名），取不与任何
  现存或已分配键冲突的最小 `n`（从 2 起）。

## 引用同步更新

- 相对说明符（`./`、`../`）基于 importer 目录按词法解析；其余说明符先尝试
  别名展开，无法展开者视为外部包并忽略。
- 引用解析到冲突组时，用大小写敏感的精确匹配消歧（先完整路径、后省略扩展名）。
- 目标模块被改名时重写说明符，保持原风格：相对引用仍为相对路径、别名引用
  仍为别名形式、原本省略扩展名则仍省略。
- 无法解析或仍歧义的引用在 `badReferences` 中报告，不做猜测性修改。

## 输入格式

```json
{
  "aliases": { "@app/*": "src/*" },
  "extensions": [".ts", ".js"],
  "modules": [{ "path": "src/Foo.ts", "renameCost": 10 }],
  "references": [{ "from": "src/main.ts", "specifier": "./foo" }]
}
```

## 使用

```sh
bun test                          # 运行测试
bun run src/cli.ts input.json     # 输出 JSON 报告；存在冲突时退出码为 1
cat input.json | bun run src/cli.ts   # 也可从 stdin 读取
```

输出包含 `conflicts`、`renames`、`totalCost`、`updatedReferences`、
`badReferences` 五个部分，示例见 `examples/input.json`。

## 目录结构

- `src/pathRules.ts` — 题内路径规则（规范化、别名、扩展名、折叠）
- `src/resolver.ts` — 模块索引与引用解析/渲染
- `src/analyzer.ts` — 冲突检测、最小代价改名、引用同步
- `src/cli.ts` — 命令行入口
- `test/` — bun:test 测试
