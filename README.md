# 关系分解检查器

给定关系模式 R、函数依赖集 F 与分解 ρ，本检查器独立判定两项性质：

1. **无损连接** —— 通过有限表格追逐（tableau chase）判定；
2. **依赖保持** —— 通过投影闭包迭代判定，不保持的依赖给出闭包缺口。

两项性质相互独立：**依赖保持成立不代表无损连接成立，反之亦然**，检查器分别独立判定，绝不互相推导。

## 构建与运行

```bash
# 编译（需要任意 TypeScript 4.x+ 编译器）
tsc -p tsconfig.json

# 运行检查
node dist/src/cli.js examples/textbook-5way.json
node dist/src/cli.js --json examples/lossless-preserving.json
cat input.json | node dist/src/cli.js -

# 测试
node dist/test/run-tests.js
```

## 输入格式

```json
{
  "attributes": ["A", "B", "C"],
  "fds": ["A->B", { "lhs": ["B"], "rhs": ["C"] }],
  "decomposition": [["A", "B"], ["B", "C"]]
}
```

- 属性最多 **10 个**，超出即拒绝；
- FD 支持字符串形式（`A->BC`、`DE->C`，按已知属性名切分）或对象形式；
- 分解必须覆盖全部属性，未知属性、重复属性均被拒绝。

## 判定方法

### 无损连接：有限表格追逐

- 初始表格每个分解子模式一行：子模式内属性填 distinguished 符号 `aⱼ`，其余填非 distinguished 符号 `bᵢ,ⱼ`；
- 按 **FD 输入序 → 行对 (i<j) → 右部属性序** 的固定顺序扫描，对左部符号一致的所有行对合并右部符号，直至不动点；
- 合并规则确定：distinguished 符号优先；两个非 distinguished 符号合并时保留行号小者（再按列号）；
- 最终表格出现全 distinguished 行 ⟺ 无损连接。

### 依赖保持：投影闭包迭代

对每条 FD `X→Y`，令 `Z := X`，反复对每个子模式 Rᵢ 执行
`Z := Z ∪ ((Z ∩ Rᵢ)⁺_F ∩ Rᵢ)` 至不动点。`Y ⊆ Z` 则保持；否则报告
**闭包缺口** `Y ∖ Z` 及可达闭包 `Z`。

## 确定性与可重放

- 所有迭代顺序固定（输入顺序），符号合并规则确定，同一输入必产生完全相同的合并步骤序列；
- 每次检查内部执行两次独立追逐并比对步骤序列，报告中输出可重放性自检结果；
- 每一步合并记录完整证据：触发的 FD、行对、左部符号快照、被合并符号与保留符号。

## 示例

- `examples/lossless-preserving.json` —— 无损且保持；
- `examples/lossless-not-preserving.json` —— 无损但不保持（B→C 闭包缺口 {C}）；
- `examples/preserving-but-lossy.json` —— 保持但有损（保持 ≠ 无损）；
- `examples/textbook-5way.json` —— 教科书五路分解：9 步追逐后无损，但 A→C、B→C、CE→A 不保持。
