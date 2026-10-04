# fix-combiner：自动修复组合工具

从一组自动修复建议中选出**总收益最大的兼容子集**（默认最多 25 项），
模拟应用后重新检查前置条件，并为每个未选中的建议输出冲突/排除证据。

核心原则：**单独适用不代表合在一起也适用**，兼容性绝不只按“文本范围不重叠”判断。

## 运行环境

- Node.js ≥ 23.6（直接运行 TypeScript，无需编译与第三方依赖）

```bash
npm test                          # 运行测试
node src/cli.ts examples/sample.json            # 运行示例（含应用后文件内容）
node src/cli.ts examples/sample.json --no-files # 只输出选择与证据
```

## 兼容性判定（三层）

1. **文本编辑重叠**（`src/ranges.ts`）：同一文件内替换区间相交、插入点落在他人
   替换区间内部或起点、同一位置两个插入，均判冲突。
2. **语义冲突**（`conflictKeys`）：文本范围完全不重叠、但声明了相同冲突标签的
   建议互斥（例如对同一代码的两种互斥重构方案）。
3. **前置条件复查**（`src/select.ts` 的模拟应用）：一个建议的编辑可能使另一个
   建议的前置条件失效（读写干涉）。选中集合按 `requires` 拓扑排序后逐个模拟
   应用，每次应用前在当前文件状态下重新求值前置条件；暂不满足的延迟重试到
   不动点，仍失败的剔除、记录证据并重解。

## 选择算法

`src/solver.ts` 实现分支定界：

- 冲突图上两两互斥（硬冲突：编辑重叠 + 语义冲突标签）；
- `requires` 蕴含：选中一个建议必须同时选中其前置闭包（环合法，整体取舍）；
- 基数不超过 `maxSelected`（默认 25）；
- 目标：最大化总收益。设有搜索节点预算，超预算时返回当前最优并标记
  `optimal: false`。

## 未选中建议的证据

每个未选中的建议都带有 `reasons`：

- `conflict`：与已选建议的冲突证据（重叠的文件与范围、共享的语义冲突标签）；
- `precondition-failed-after-apply`：应用所选集合后前置条件失效，附失效断言
  详情与造成失效的已应用修复列表（`invalidatedBy`）；
- `requires-excluded`：前置修复未被选中，附依赖链；
- `capacity`：无直接冲突，但在 25 项上限下选入它不能提升总收益；
- `invalid`：输入非法（编辑指向不存在的文件、范围越界、requires 悬空等）。

## 输入格式

```jsonc
{
  "files": { "src/a.ts": "文件内容..." },
  "maxSelected": 25,                 // 可选，默认 25
  "suggestions": [
    {
      "id": "fix-1",
      "description": "……",
      "benefit": 8,                  // 非负收益
      "edits": [
        {
          "file": "src/a.ts",
          "range": { "start": { "line": 0, "character": 4 },
                     "end":   { "line": 0, "character": 7 } },
          "newText": "替换文本"       // start == end 表示插入
        }
      ],
      "preconditions": [             // 应用前必须成立（会在应用后复查）
        { "kind": "contains", "file": "src/a.ts", "pattern": "foo" },
        { "kind": "notContains", "file": "src/a.ts", "pattern": "bar" },
        { "kind": "matches", "file": "src/a.ts", "regex": "x", "maxCount": 1 },
        { "kind": "fileExists", "file": "src/b.ts" }
      ],
      "requires": ["fix-0"],         // 可选：前置修复
      "conflictKeys": ["scheme-a"]   // 可选：语义冲突标签
    }
  ]
}
```

## 代码结构

- `src/types.ts` — 数据模型
- `src/ranges.ts` — 位置/偏移换算与编辑冲突判定
- `src/preconditions.ts` — 前置条件求值
- `src/apply.ts` — 编辑应用（同文件内按偏移降序）
- `src/conflicts.ts` — 两两硬冲突分析与读写干涉检测
- `src/solver.ts` — 分支定界求最大收益兼容子集
- `src/select.ts` — 编排：校验 → 冲突分析 → 求解 → 模拟复查 → 证据
- `src/cli.ts` — 命令行入口
