# 定长缓冲写入覆盖证明

代码审查需要判断输出缓冲是否在发送前被完整初始化。写供Go分析宿主调用的抽象执行包，输入有限控制流图，操作为固定区间写、条件分支、可有界循环与发送；每个发送点求所有到达路径共同已写的区间，动态写入由输入给出上下界，不把可能写当成必然写。返回未初始化区间和反例路径，预算不足标未知。用小缓冲具体枚举核对合流、零次循环及部分覆盖。

## 包 `absexec`

入口 `absexec.Analyze(absexec.Request{CFG, Budget})`，返回 `absexec.Report`。

- `CFG`：有限控制流图，`Node` 支持 `OpWrite`（固定区间 `[Lo,Hi)` 必写）、
  `OpDynWrite`（动态写，仅 `[LoMax,HiMin)` 计入必写）、`OpBranch`（两个后继
  都视为可行）、`OpSend`（检查所需区间，默认整缓冲）与 `OpNoop`。
- 有界循环用 `BackEdge{From,To,MaxCount}` 表达，回边在单条路径上至多走
  `MaxCount` 次；计数从 0 开始，因此零次循环是一等可行路径。回边之外的图
  必须无环，否则 `Analyze` 返回校验错误。
- 分析在按 `(节点, 回边计数向量)` 展开的有限 DAG 上做数据流：每个展开点保存
  所有到达路径必写集合的交集，可能写不会提升为必然写。
- 每个发送点给出 `SendSafe`、`SendUninitialized`、`SendUnreachable` 或
  `SendUnknown`。未初始化结果按见证路径分组，附该路径的节点序列与路径上
  全部未写区间。
- `Budget` 限制展开的状态数（0 表示无界）；预算耗尽时不再下未初始化结论，
  相关发送点标 `SendUnknown` 且 `Report.Complete=false`。

## 验证

`go test ./absexec` 内含一个独立的蛮力路径枚举器 `crosscheck_test.go`，
在小缓冲上穷举所有有界路径并逐字节核对 must-written 交集，覆盖合流、
零次循环、部分覆盖、动态写边界、嵌套循环与预算耗尽场景；每条反例路径
都会被独立重放，确认其确为合法 CFG 路径且确实未写所报字节。
