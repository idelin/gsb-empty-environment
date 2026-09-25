# accname-incremental

简化的可访问名（accessible name）/ 可访问描述（accessible description）计算与增量重算引擎。
节点树与字形度量均由夹具给出，核心计算不借助浏览器布局引擎。

## 运行要求与测试

- Node.js >= 22.18（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需构建步骤）
- `npm test`（即 `node --test "test/*.test.ts"`，Node 内置测试运行器）

## 结构

- `src/metrics.ts` — 字形度量夹具接口；前进宽度为 0 的字符视为不渲染
- `src/tree.ts` — DOM 式节点树；变更以依赖键形式累积（`attr:/role:/text:/hidden:/children:/idref:/labelfor`）
- `src/accname.ts` — 纯函数式名称/描述计算，计算过程登记依赖
- `src/engine.ts` — 缓存 + 依赖索引 + 增量重算 + 变化通知

## 名称来源优先级（高 → 低）

1. 角色禁止命名（`presentation`/`none`/`paragraph` 等，仅根节点判定）→ 空名
2. `aria-labelledby`：可跨节点链式跟随；被显式引用的隐藏节点照常计入；
   visited 集合做循环检测；引用全部悬空或成环时回退到后续来源
3. `aria-label`
4. 原生标签：`img`/`input` 的 `alt`、`label[for]` 关联
5. 内容派生：仅限允许从内容命名的角色（或被引用/遍历途中）；
   未显式引用的隐藏内容跳过；嵌套控件贡献其替代文本（如按钮内 `img` 的 `alt`）
6. `title` 兜底

描述来源：`aria-describedby` → `aria-description` → `title`。

## 增量模型

- 每次计算把读取到的节点属性/角色/文本/隐藏状态/子结构及 id 解析登记为依赖键。
- DOM 式变更（`setText`/`setAttribute`/`setRole`/`setHidden`/`appendChild`/`removeNode` 等）
  使树版本 +1 并累积变更键；`engine.sync()` 仅失效依赖受影响的缓存条目，
  原地重算并对结果发生变化的节点输出 `ChangeNotification`。
- 未受影响的条目通过依赖校验后随版本前滚，继续命中缓存。
- 缓存按（引擎实例, 树版本）隔离：一棵树的悬空/错误引用不会串到其他实例；
  悬空引用登记 `idref:` 依赖，日后补上对应 id 的节点时会触发“治愈”重算。

实际测试运行结果见 `RESULTS.md`。
