# 控制依赖让秘密流入公开输出

安全编译器只传播显式数据污点时，会漏掉由秘密分支决定的公开常量输出。为Go分析宿主实现可导入的有限程序信息流包：程序为至多48节点的控制流图，寄存器取8位值并带秘密标签集合，指令含常量、复制、模256算术、相等分支、select、publicWrite和declassify。显式运算标签取输入并集；分支条件标签形成程序计数标签并沿两后继传播，直到它们的最近公共后支配节点才移除；不可约图没有唯一后支配汇合时标签保持到退出。publicWrite的值标签或当前程序计数标签非空即报告泄漏。declassify只移除指令声明且在宿主白名单中的标签，不能清除程序计数标签，并记录审计事件。实现后支配分析与标签不动点，循环和return、无后继节点和异常退出都连到虚拟出口；分支两个后继相同不引入新的程序计数标签，异常边上的条件标签照常传播；预算耗尽返回未知。另提供至多3个秘密布尔输入的小域具体执行参考，枚举两两秘密变化且公开输入相同的输出轨迹，核对分析不能漏掉真实差异，同时允许保守误报。覆盖分支两侧写相同常量、提前return、循环条件和分支后select，不能把控制相关语句数当作泄漏证据。

## Package

Import path: `example.org/netsec/q0055`, package `netsec`.

The main entry points are:

- `Analyze(program any, options ...any) Result` and `AnalyzeWithOptions`.
- `PostDominance(program any) (PostDomInfo, error)`.
- `RunConcrete(program any, secretValues map[Register]uint8, configs ...ExecConfig) ConcreteResult`.
- `CrossCheck(program any, options ...any) CheckReport` (alias `Verify`).

`Analyze` accepts either `Program` or `*Program`. Options may be supplied as
`Options`, a `LabelSet`/`[]Label`/`[]string` whitelist, or a positive integer
analysis budget.

## Program model

- A `Program` has at most `MaxNodes` (48) nodes. Node zero is the default
  entry.
- Registers hold `uint8` values. `OpAdd`, `OpSub`, and `OpMul` wrap modulo
  256.
- Instructions are `OpConst`, `OpCopy`, `OpAdd`, `OpSub`, `OpMul`,
  `OpEqBranch`, `OpSelect`, `OpPublicWrite`, `OpDeclassify`, `OpReturn`, and
  `OpAbort`.
- `OpEqBranch` has exactly two successors: successor zero is the true edge,
  successor one is the false edge.
- `OpSelect` computes `dst = src1 != 0 ? src2 : src3`.
- `OpPublicWrite` appends `src1` to the public output named by `Output`.
- Successors can be given as `Node.Succ`, `Node.Successors`, or
  `Node.Edges`. An edge is abnormal when `Edge.Kind == AbnormalEdge` or
  `Edge.Abnormal` is true. Abnormal and ordinary edges propagate the same
  control labels.
- `Node.Return`, `Node.AbnormalExit`, `OpReturn`, and `OpAbort`, as well as
  nodes with no successor, are connected to a virtual exit for
  post-dominance. An infinite loop that cannot reach any exit gets a
  conservative virtual-exit edge as well.

Example:

```go
p := netsec.NewProgram()
p.Inputs = []netsec.Input{
    {Register: 0, Labels: netsec.Labels("secret")},
    {Register: 1, Value: 0},
}
p.AddNode(netsec.NewEqBranch(0, 1), 1, 2) // secret == 0 ?
p.AddNode(netsec.NewConst(2, 1), 3)       // true arm
p.AddNode(netsec.NewConst(2, 2), 3)       // false arm
p.AddNode(netsec.NewPublicWrite(2), 4)    // control-dependent write
p.AddNode(netsec.NewReturn())

result := netsec.Analyze(p)
if result.Unknown {
    // budget exhausted or invalid program
}
if result.HasLeak() {
    // result.Leaks identifies the write and the value/PC labels
}
```

## Label analysis

- Explicit data labels are the union of instruction inputs. Assignments made
  while a control token is active also carry the active PC labels, which is
  what catches a value selected after a secret branch join.
- A branch condition with non-empty labels creates a PC token that follows
  both successors. The token is removed at the nearest common
  post-dominator of the two successors.
- If both branch successors are the same node, no new PC token is created.
- If no unique nearest common post-dominator exists (for example an
  irreducible merge), the token remains active through the virtual exit.
- A public write is rejected if its value labels or its active PC labels are
  non-empty.
- `OpDeclassify` removes only the intersection of the labels named by the
  instruction and the host whitelist. Active PC tokens are never removed.
  Every declassify instruction on a reachable node produces an audit event
  (`Result.Audit`) with requested, removed, remaining, and PC labels.
- `Options.Budget` bounds label-fixpoint work. Exhaustion sets
  `Result.Unknown` and `Result.Err = ErrBudgetExceeded`; it never reports the
  program safe.

The analysis is deliberately conservative. In particular, two arms that
write the same public constant still produce a static leak, because the write
is control-dependent. The differential checker below distinguishes that
permitted false positive from a real leak.

## Differential reference

`CrossCheck` enumerates all `0/1` assignments for at most three secret input
registers declared in `Program.Inputs`. Public input values stay fixed for
every pair. Each pair is executed with eight-bit concrete semantics and its
public output trace is compared.

- A sensitive trace difference that the static analysis did not flag is
  reported in `CheckReport.Missed`; this is the soundness failure.
- Equal traces with a static leak are reported in `Conservative` and are
  allowed.
- Differences that occur only in declassified outputs are reported in
  `Declassified` and are allowed.
- `CheckReport.Sound()` is true exactly when execution and analysis
  completed and `Missed` is empty.

The reference executor also enforces a step limit. Exhaustion returns
`ConcreteResult.Unknown` and makes the affected comparison inconclusive.
