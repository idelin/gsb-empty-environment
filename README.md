# batch-spell-replace

批量拼写替换库（TypeScript）。所有规则同时针对**原始文本**匹配，替换结果不会触发后续规则（无级联）。

## 特性

- **词边界匹配**：`cat` 不会命中 `concatenate`；词字符为 Unicode 字母/数字/下划线（可自定义）。
- **有限大小写映射表**：默认表覆盖 `lower` / `upper` / `capitalized` / `other` 四种有限样式，按被匹配文本的大小写风格重写替换词；表中缺失的样式保持替换词原样。`caseSensitive: true` 的规则跳过映射表。
- **冲突解决**：重叠候选按 `priority`（高者优先）再按规则 `id`（小者优先）裁决；败者作为 `shadowed` 输出，保留原区间 `[start, end)`。
- **零长度禁用**：`pattern` 为空或 `enabled: false` 的规则被禁用并列入 `disabledRules`。
- **跨 rope 块匹配**：`Rope` 以块存储文本，匹配可跨越块边界；块边界不会被误认为词边界。
- **反向补丁**：`invertEdits` 生成新文本坐标系下的逆编辑，应用后还原原文。

## 用法

```ts
import { replace, Rope, editsFromCandidates, applyEdits, invertEdits } from "./src";

const result = replace(Rope.of("the qu", "ick fox"), [
  { id: 1, pattern: "quick", replacement: "swift", priority: 1 },
]);
result.text;       // "the swift fox"
result.adopted;    // 被采用的候选（含原区间）
result.shadowed;   // 被遮蔽的候选（含原区间）

const edits = editsFromCandidates(result.adopted);
const inverse = invertEdits(original, edits);
applyEdits(result.text, inverse); // === original
```

## 测试

```sh
bun test
```
