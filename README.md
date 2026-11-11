# 抽象反例逐步精化

静态检测给出的危险路径可能因抽象合并而不可行。给Go分析宿主写可导入的精化组件，输入有限布尔程序与初始谓词集合，检查抽象可达反例后用具体枚举判可行；不可行时加入区分冲突状态的最小谓词，再重建受影响摘要。最多十二变量，预算耗尽返回未知。用伪反例与真实反例交替出现的程序验证。

## 组件

包 `netsec`（模块 `example.org/netsec/q0405`）暴露：

- `Program` / `Node` / `Expr` / `Stmt`：有限布尔程序模型，变量数上限 `MaxVariables`（12），具体状态空间至多 4096，可直接枚举。
- `Verify(prog, initialPredicates, opts) *Result`：CEGAR 主入口，三值结论 `Safe` / `Unsafe` / `Unknown`。
- `Result.Events`：按时间顺序记录 `EventAbstractCE`、`EventSpuriousCE`、`EventRealCE`、`EventRefinement`，可观察伪反例与真实反例交替。
- `Options`：枚举总量、谓词数、精化轮数三类预算；耗尽即返回 `Unknown` 并给出 `Reason`。

## 精化流程

1. 在当前谓词集合上做抽象不动点可达分析，节点摘要（入口立方 → 出口/失败立方）按节点缓存。
2. 错误节点抽象可达时，沿重建路径逐节点枚举具体取值集合，判定反例是否具体可行。
3. 伪反例定位首个断链节点，取该节点具体可达取值 good 与同抽象立方内导向错误的取值 bad。
4. `learnMinimalSeparator` 用贪心决策树学习在 good 上恒真、在 bad 上恒假的最小合取谓词（含局部冗余消除）。
5. 加入新谓词后，前向受影响区域的摘要丢弃重算，区域外摘要在新谓词下无损重编码。

## 验证

`refine_test.go` 覆盖伪反例精化到安全、真实反例见证、伪-真交替、多谓词合取、循环不动点、12 变量全枚举以及预算耗尽等场景：

```sh
go test ./...
```
