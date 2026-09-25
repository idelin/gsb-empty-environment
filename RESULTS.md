# 实际运行结果

- 运行时间：2026-09-25（Asia/Shanghai）
- 运行时：Node.js v23.9.0（原生 TypeScript 类型剥离），`node:test` 内置测试运行器
- 命令：`node --test test/*.test.ts`
- 结果：**15 通过 / 0 失败**（duration_ms ≈ 1955）

```
✔ 不适用不覆盖允许（deny-overrides / permit-overrides / first-applicable）
✔ first-applicable 顺序有意义
✔ 交换律组合（deny/permit-overrides）不受遍历顺序影响
✔ deny-overrides 的 Deny 结论同样与遍历顺序无关
✔ 两个允许分支给出不兼容输出格式 → 义务冲突，不被允许结论掩盖
✔ 第二义务执行失败 → 已应用义务按逆序补偿
✔ 多个已应用义务严格按逆序补偿
✔ 补偿失败保留可重试状态，retry 从断点恢复
✔ 限额取更严（min），字段遮蔽取并集，输出格式相同则去重
✔ 合并满足交换律与结合律（结果与遍历顺序无关）
✔ 不兼容输出格式产生确定性冲突，与顺序无关
✔ 旧版本属性回包：v1 回包求值、原子切换到 v2、历史决策按当时版本重放
✔ 模块环：激活时检测委托环并原子拒绝，已激活版本不受影响
✔ 解释证据缺失：无描述符属性 → Indeterminate 并列出缺失证据；缺值单列
✔ 委托模块提供解释证据：描述符经 delegates 导入后可正常求值
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

备注：环境内 Node 16 无 `node:test`，且网络受限无法安装 `typescript`，故采用 Node 23 原生类型剥离直接运行 `.ts`（源码仅使用可擦除类型语法，`tsconfig.json` 已开启 `erasableSyntaxOnly` 约束）。
