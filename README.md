# access-audit

对照"声明的访问清单"审计"实际发生的访问"，记录完整的符号链接展开链，
找出未声明的依赖并生成最小补充清单。使用 TypeScript 实现（可擦除语法，
Node >= 22.6 直接运行，无需编译步骤）。

## 用法

```sh
node src/cli.ts audit --manifest declared.json --trace accesses.json \
    [--case sensitive|insensitive|auto] [--base <dir>] \
    [--report report.json] [--supplement supplement.json]
```

- 退出码：`0` 全部已声明；`2` 存在未声明/不可验证访问；`1` 用法或输入错误。
- `--base` 决定相对路径的基准目录，默认为清单所在目录。
- 演示数据见 `examples/`，测试见 `test/`（`npm test`）。

## 清单格式（declared.json）

```json
{
  "casePolicy": "auto",
  "files": ["config/app.yaml"],
  "directories": ["data/"],
  "executables": ["tools/build.sh"]
}
```

- `files`：授权对指定文件的读/写（按词法路径或符号链接解析后的真实路径匹配）。
- `directories`：授权目录子树的读/写，**但符号链接跳出目录的目标不自动获得授权**——
  访问必须同时满足"词法路径在目录内"且"解析后的真实路径仍在目录内"。
- `executables`：仅授权 `exec` 类访问；目录声明永不授权执行。
- `casePolicy`：`sensitive` / `insensitive` / `auto`（默认 `auto`，
  通过在基准目录实际探测文件系统得出，适配 macOS/Windows 的大小写不敏感卷）。

## 访问轨迹格式（accesses.json）

```json
[
  { "path": "data/input.csv", "kind": "read" },
  { "path": "tools/build.sh", "kind": "exec" },
  "data/other.txt"
]
```

`kind` 为 `read`（默认）/ `write` / `exec`；也接受纯字符串简写。

## 审计语义

- **符号链接展开链**：每次访问逐级解析路径中的每个符号链接组件，
  记录完整跳转链（`hops`），支持链接链、绝对/相对目标、悬空链接；
  检测到循环（超过 40 跳）时标记为 `unverifiable`，绝不误判为已授权。
- **不存在路径**：不报错；从第一个缺失组件起按词法补全，记录
  `missingAt`，照常参与声明匹配。
- **重复访问**：按（词法路径 + 解析目标）折叠，保留访问次数与合并后的
  操作类型，统计中单独报告折叠数量。
- **大小写策略**：所有路径比较（含目录包含判断）都经过策略归一化。

## 最小补充清单

对每个未声明访问生成覆盖它的最小条目集：

- 同一父目录下 ≥2 个未声明兄弟路径折叠为一条 `directories` 条目，
  但仅当该目录的所有直接子项都在未声明集合内（避免过度授权无关内容）；
- 其余以单独 `files` 条目输出；`exec` 访问始终以单独 `executables`
  条目输出，绝不折叠进目录；
- 补充清单使用解析后的真实路径，合并回主清单后再次审计应无遗漏
  （符号链接逃逸与循环除外，它们需要人工处置）。

## 目录结构

- `src/resolver.ts` — 符号链接展开链解析（循环/缺失安全）
- `src/paths.ts` — 大小写策略与路径包含判断
- `src/manifest.ts` — 清单/轨迹加载与声明预解析
- `src/auditor.ts` — 匹配核心（含目录逃逸判定）与重复折叠
- `src/minimize.ts` — 最小补充清单生成
- `src/cli.ts` — 命令行入口
