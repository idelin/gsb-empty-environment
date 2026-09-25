# 实际运行结果

- 日期：2026-09-26 06:25:54 CST
- Node.js：v23.9.0（原生 TypeScript 类型擦除，无构建步骤）
- 命令：`npm test`（即 `node --test "test/*.test.ts"`）
- 退出码：0

```

> accname-incremental@0.1.0 test
> node --test "test/*.test.ts"

(node:50060) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ aria-labelledby 链跨多个节点且含环时按 visited 集合终止并回退 (49.431708ms)
✔ aria-labelledby 多级链取到最深节点的文本 (0.215166ms)
✔ 多个 IDREF 按序拼接，悬空引用贡献为空且不抛错 (0.102334ms)
✔ 隐藏但被显式引用的内容计入名称，普通隐藏内容跳过 (0.159084ms)
✔ 零宽字形按夹具度量从渲染文本中剔除 (0.084583ms)
✔ 禁止命名的角色在根节点得空名，被引用时仍贡献文本 (0.082958ms)
✔ 名称来源优先级：labelledby > aria-label > 原生标签 > 内容 > title (0.112459ms)
✔ 原生标签：label[for] 关联与嵌套控件替代文本 (0.098125ms)
✔ 描述优先级：describedby > aria-description > title，链含环时终止 (3.622625ms)
(node:50062) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ 修改被多处引用的标签：全部相关节点收到通知，无关节点不重算 (13.474875ms)
✔ 无关文本变更不触发任何通知与重算 (0.190625ms)
✔ 隐藏状态切换只影响把该节点当内容的父节点，显式引用方不受影响 (0.426583ms)
✔ 错误引用不串到其他实例：缓存按树版本隔离 (0.24475ms)
✔ 被移除节点的缓存条目被清除并通知 (0.161542ms)
(node:50063) ExperimentalWarning: Type Stripping is an experimental feature and might change at any time
(Use `node --trace-warnings ...` to show where the warning was created)
✔ 重复 ID：解析取文档序首个；移除首个后落到第二个，与全量参考一致 (23.069833ms)
✔ 节点移除：被引用的中间节点删除后链回退，与全量参考一致 (1.108166ms)
✔ 角色改变：禁止命名 ↔ 可命名，与全量参考一致 (3.019542ms)
✔ 嵌套控件替代文本：alt 变更驱动按钮名更新，与全量参考一致 (0.460792ms)
ℹ tests 18
ℹ suites 0
ℹ pass 18
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 878.941875
```
