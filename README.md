# bundle-guard

资源打包工具的模块命名冲突检测器：按题内规则计算路径等价，检测冲突，给出
总代价最小的改名建议，并同步更新模块间的引用（import 说明符）。

## 路径等价规则（纯字符串计算，不依赖宿主文件系统）

对每条模块路径依次应用以下规则，得到**规范键（canonical key）**；两条路径
等价当且仅当规范键相同：

1. **分隔符统一**：`\` 视为 `/`，忽略空段。
2. **别名展开**：按路径段做最长前缀匹配（如 `@app` → `src/app`），替换前缀。
3. **词法规范化**：解析 `.` 与 `..`（纯字符串；越过根的 `..` 保留）。
4. **扩展名省略**：末段的已知扩展名省略（默认 `.ts .tsx .js .jsx .mjs .cjs .json`，可配置）。
5. **索引文件省略**：末段为 `index` 时省略（`foo/index.ts` ≡ `foo`，可配置）。
6. **大小写折叠**：全路径小写化（可配置为大小写敏感）。

另定义**字面键（literal key）**：只做规则 1–3（不省略扩展名、不折叠大小写），
用于识别"同一个文件"（重复）与精确匹配引用。

## 冲突与重复

- **重复（duplicates）**：字面键相同 → 同一路径被重复声明，单独报告，不参与改名。
- **冲突（conflicts）**：规范键相同但字面键不同 → 同一冲突组。

## 最小改名建议

对大小为 k 的冲突组，至少需改名 k−1 个模块。算法保留**改名代价最大**的模块
（平手取路径字典序最小者），其余按代价升序依次改名，因此总代价达到理论下界
（组内总代价 − 组内最大代价，逐组求和）。

新名字在原目录下生成：`stem_2.ext`、`stem_3.ext`……取第一个规范键未被占用
（不被任何现有模块或已分配的改名结果占用）的候选，保证结果确定性且无新冲突。

改名代价由输入的 `renameCost` 给出（默认 1，必须是非负有限数）。

## 引用同步更新

引用（`{ from, specifier }`）按如下规则解析与重写：

- `./`、`../` 开头 → 相对引用，以引用者所在目录为基解析；
- 命中别名前缀 → 别名引用；
- 否则 → 外部包（`external`），不处理。

解析后先做**字面键精确匹配**（如 `./foo.js` 精确指向 `src/foo.js`），失败再
退到规范键匹配：

- 唯一命中 → 若目标或引用者被改名，输出 `newSpecifier`（`status: "updated"`）；
- 多个命中 → `status: "ambiguous"`，列出候选，不自动改写；
- 无命中 → `status: "unresolved"`。

重写规则：别名引用优先用（最长匹配的）别名重建，失败回退为相对路径；相对
引用按新位置重新计算；原说明符带扩展名则新说明符保留扩展名，否则省略；显式
的 `index` 末段保留。引用者自身被改名时，其新路径体现在 `newFrom` 字段。

`ok` 为 true 表示：无重复、无歧义/未解析引用，应用 `renames` 后即可安全打包。

## 输入 / 输出

```jsonc
// 输入
{
  "modules": ["src/main.ts", { "path": "src/Foo.ts", "renameCost": 5 }],
  "references": [{ "from": "src/main.ts", "specifier": "./foo.js" }],
  "options": {
    "aliases": { "@app": "src/app" },
    "extensions": [".ts", ".js"],
    "caseSensitive": false,
    "resolveIndexFiles": true
  }
}
// 输出 AnalysisReport：见 src/types.ts
```

## 使用

```bash
npm test                      # 运行测试（无需任何依赖）
npm run cli -- examples/input.json   # 或: node scripts/cli.mjs input.json
```

在有网络的环境可 `npm i -D typescript @types/node && npm run build` 用 `tsc`
做完整类型检查并产出 `dist/`（`tsconfig.json` 已就绪）。

说明：本仓库源码为标准 TypeScript（仅使用可擦除类型语法）。由于沙箱环境无法
安装 `tsc`，`scripts/strip-types.mjs` 提供了一个仅做类型擦除的本地构建，
将 `src/`、`test/` 转换到 `.build/` 后在 Node 上运行，逻辑与 `tsc` 编译结果一致。
