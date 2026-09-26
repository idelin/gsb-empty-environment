# pagination-engine

约束感知的分页引擎。节点树与字形度量全部由夹具（`src/fixtures.ts`、测试内联夹具）给出，
核心计算（断行、分页、脚注、验证）不借助任何浏览器布局引擎。需要 Node.js >= 23
（直接以类型擦除方式运行 TypeScript，无需构建步骤）。

## 运行

```sh
npm test    # Node.js 内置测试运行器（node:test），18 个测试
npm start   # 运行样例：分页 + 证书 + 内容更正级联 + 穷举验证
```

## 设计

- **断行**（`src/linebreak.ts`）：按夹具字形度量贪心断行；`[^id]` 脚注引用零宽，
  归属到其所在行；超长词显式报告（`long-word` 降级），不静默截断。
- **分页**（`src/paginate.ts`）：记忆化 DP 试排回滚。候选分页点逐一试排、
  死路回滚（`stats.candidatesTried/Pruned` 可查）。目标函数 = 空白代价
  （`blankCostWeight * slack^2`）+ 换页罚分 + 段落跨页断开罚分，末页空白免费。
  - 孤行/寡行最小行数（默认两侧各 2 行）：两行段落绝不 1/1 拆分。
  - 标题跟随（keep-with-next）：标题页不得以其结尾。
  - 不可拆块：超高块显式报告 `block-overflow` 降级并标记溢出页。
  - 约束不可满足时：显式报告 `constraint-relaxed` / `footnote-overflow` 降级，
    绝不默默违规；任何路径都不会产生空白页。
- **脚注与不动点**（`src/fixedpoint.ts`）：脚注随引用页占空间（含每页一次的分隔线
  高度）。每次试排后重新求脚注高度，高度变化则回滚重排，直到不动点；
  签名循环或超出迭代上限时明确报告无解。`applyCorrection` 支持局部内容更正
  （如脚注变高），级联推迟正文。
- **证书**（`src/certificate.ts`）：逐页列出容量、正文/脚注占用、空白、
  孤行寡行/标题跟随/脚注归属的独立复检结果、被放松的约束与全部降级记录。
- **穷举验证**（`src/exhaustive.ts`）：小文档枚举全部合法分页，证明 DP 代价最优，
  结果写入证书 `optimality` 字段。
