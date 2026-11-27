现代托管与静态编译器的标量替换（SROA）依赖精确的逃逸分析把未逃逸的堆/栈结构体分配彻底消除并拆成独立 SSA 寄存器变量，一旦逃逸传递或别名边界漏判就会静默破坏内存语义：1）对象 `A` 的引用若被写入另一对象 `B` 的字段中，则当 `B` 经返回值、全局存储或不可见外部调用逃逸时，必须沿字段指向图（Points-To / Connection Graph）传递标记 `A` 一并逃逸；2）跨过程分析必须为被调函数构建参数逃逸摘要（区分参数仅被读取 `NoEscape`、经返回值返回 `ReturnEscape`、存入另一参数 `ArgToArg(i -> j)` 或全局逃逸 `GlobalEscape`！），支持直接与间接递归函数的不动点迭代摘要计算；3）对于确认不逃逸的聚合分配，若某分支路径上仍取了其指针存入局部条件 `phi` 指针变量并经该 `phi` 读写字段，只有当该 `phi` 全部来源均为已知非逃逸静态偏移或可安全拆分时才允许标量替换，否则遇到动态偏移、不同大小部分重叠写或未知别名必须保守拒绝 SROA！4）执行 SROA 后在合并基本块自动为被拆分的每个字段插入规范 SSA `phi` 节点并移除原分配指令。为编译器中端交付可导入的 Go 跨过程逃逸分析与 SROA 优化核，并内置多函数带堆/栈/全局存储的 IR 解释器。验收覆盖递归跨过程参数摘要不动点、`ArgToArg` 嵌套逃逸传递、分支内局部指针 `phi` 别名识别、动态偏移/重叠写保守禁优化、成功 SROA 消除堆分配计数以及解释器优化前后全输出与堆状态逐项等价。

---

## 实现概览

所有代码位于可导入包 `midend`（无第三方依赖，仅用标准库）。

| 文件 | 职责 |
| --- | --- |
| `ir.go` | 类型系统、SSA 操作数、指令集（`Alloc/FieldAddr/AddrAdd/Load/Store/Phi/Call/ExtCall/...`）、终结符与 `Program` |
| `builder.go` | 流式 IR 构建器 `Builder` |
| `escape.go` | 跨过程逃逸分析：连接图（Points-To + 字段边）、参数摘要、单调不动点迭代 |
| `cfg.go` | CFG、逆后序、支配树（CHK 迭代算法）与支配边界 |
| `sroa.go` | 指针描述符分析、SROA 资格判定、支配边界 phi 放置、mem2reg 风格重命名 |
| `interp.go` | 字节寻址堆/栈/全局存储的 IR 解释器，可观察输出与 BFS 规范化堆快照 |
| `clone.go` | 程序深拷贝（优化不修改入参）与操作数重写工具 |
| `types.go` / `print.go` | 寄存器类型推导、IR 文本打印 |

### 公开 API

- `AnalyzeEscape(prog) *EscapeResult`：返回 `Summaries()`（每个函数的 `ParamSummary`，分类为
  `NoEscape / ReturnEscape / ArgToArg / GlobalEscape`）与 `AllocEscaped(fn, name)`。
- `RunSROA(prog, esc) (*Program, *sroaResult)`：返回深拷贝后的优化程序与统计
  （`EliminatedHeapAllocs`、`PromotedStackAllocs`、`Rejected` 拒绝原因）。
- `NewInterpreter(prog).Run(args...)`：执行 IR，提供 `Output` 与 `HeapSnapshot()`。

### 关键正确性策略

- 逃逸沿连接图字段边做不动点闭包：被引用对象随引用宿主经返回值、全局存储或外部调用一并逃逸。
- 摘要格是单调的（`NoEscape < ArgToArg < ReturnEscape < GlobalEscape`），因此直接/间接递归必然收敛。
- SROA 仅在「不逃逸 + 全静态偏移 + 无动态偏移/部分重叠写/未知别名 + 指针 phi 同源」时启用；
  否则保守拒绝。非逃逸但不可拆分者安全降级为栈提升。
- 标量化后按支配边界为每个字段插入规范 SSA `phi`（含循环携带 phi），再删除原 `Alloc` 与
  仅服务于该分配的取址/读写指令。

### 验收映射（`go test ./...`）

| 验收点 | 测试 |
| --- | --- |
| 递归/互递归参数摘要不动点 | `TestRecursiveSummaryFixpoint` |
| `ArgToArg` 摘要与嵌套逃逸传递 | `TestArgToArgSummary`、`TestArgToArgNestedEscape` |
| 字段图多级逃逸闭包 | `TestEscapeFieldGraph`、`TestEscapeFieldGraphDeep` |
| 分支局部指针 `phi` 可拆分 | `TestPtrPhiSplittable` |
| 指针 `phi` 混源保守拒绝 | `TestPtrPhiDistinctAllocRejected`、`TestUnknownAliasRejected` |
| 动态偏移/重叠写保守禁优化 | `TestDynamicOffsetRejected`、`TestOverlappingWriteRejected` |
| 全局/外部调用 `GlobalEscape` | `TestGlobalAndExternalEscape` |
| 堆分配消除计数与全量等价 | `TestMixedEliminationCountAndEquivalence`、`TestSROABasicEquivalence` |
| 循环携带 phi 的 SROA | `TestLoopSROA` |
| 栈提升与返回后回收 | `TestStackPromotionReclaimsMemory` |

等价性校验对优化前后的返回值、完整 `Output` 序列与 `HeapSnapshot()`（对象按 BFS 发现顺序
规范化编号，消除地址敏感性）逐项比较。
