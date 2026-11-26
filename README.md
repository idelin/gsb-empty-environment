# seccomp — cBPF `sock_filter` 解码与符号路径越权验证

一个零第三方依赖、可直接被 Go 容器安全审计宿主导入的包。它把真实的 8 字节
`struct sock_filter` 数组解码为指令，做完整的 cBPF 静态校验，然后沿 DAG 控制流
做**路径敏感的符号执行 + 区间/位掩码约束传播**，对照宿主声明的“禁止系统调用规格”
寻找可到达 `SECCOMP_RET_ALLOW`（或指定宽松动作）的路径，并给出**具体、可复现、
最小的 `(arch, nr, arg0..arg5)` 绕过元组**。支持多层过滤器叠加的最小返回值合并验证，
并内置小参数域全枚举交叉核验与三类真实绕过测试集。

模块路径：`formal.local/w27-a4d6d2d9d3`，包名 `seccomp`。

## 快速开始

```go
import seccomp "formal.local/w27-a4d6d2d9d3"

// raw 是连续的 8 字节 sock_filter 记录（小端）。
insns, err := seccomp.Decode(raw)

policy := seccomp.NewPolicy(
    seccomp.Deny("execve",
        []seccomp.Arch{seccomp.ArchX8664, seccomp.ArchX32},
        []uint32{59}),
    seccomp.Deny("execve-i386",
        []seccomp.Arch{seccomp.ArchI386},
        []uint32{11}),
    seccomp.Deny("bpf-marker",
        []seccomp.Arch{seccomp.ArchX8664},
        []uint32{321},
        seccomp.ArgCond{Index: 1,
            Mask:  0xffffffff00000000,
            Value: 0xdeadbeef00000000}),
)

rep, err := seccomp.Verify(insns, policy)
if err != nil { /* 程序或策略非法 */ }
if !rep.Safe {
    c := rep.Minimal
    fmt.Println(c.Tuple)          // arch=i386 nr=11 args=[...]
    ok, _ := seccomp.RunTuple(insns, c.Tuple)
    fmt.Println(seccomp.ActionName(ok)) // SECCOMP_RET_ALLOW
}
```

## 能力地图

- **指令解码**：`Instruction{Code uint16; JT,JF uint8; K uint32}` 与内核
  `sock_filter` 内存布局一致；`Decode`/`DecodeOne`/`Encode`/`EncodeAll`，
  小端 8 字节；`Disassemble` 输出 tcpdump 风格助记符。
- **完整 cBPF 指令集**：`LD/LDX/ST/STX/ALU/JMP/RET/MISC`，覆盖
  `W/H/B`、`IMM/ABS/IND/MEM/LEN/MSH`、全部 ALU 操作、
  `JA/JEQ/JGT/JGE/JSET` 的 `K/X` 形式、`RET K/A`、`TAX/TXA`。
- **静态验证** `Validate` / `ValidateStrict`：
  - 非空且 `len <= 4096`（`MaxInsns`）；
  - 操作码合法、操作数合法（`M[0..15]`、除零常量、移位 `>=32` 等）；
  - 跳转严格向前、目标不越界（cBPF 偏移为无符号跳过数，天然无向后跳转，
    仍显式校验目标并构建 CFG）；
  - `BPF_ABS` 偏移不越过 64 字节 `seccomp_data`（严格模式检查对齐）；
  - 最后一条必须是 `RET`，所有可达路径终止，`RET K` 的动作必须是已知
    `SECCOMP_RET_*`；`RET A` 默认按动态返回处理，严格模式可拒绝。
- **具体模拟器** `Run` / `RunTuple`：32 位回绕语义、除零 `X` 按内核返回
  `SECCOMP_RET_KILL_THREAD(0)`，用于复核每个反例，保证**零误报**。
- **符号引擎**：
  - 跟踪 `A`、`X`、`M[0..15]` 以及 `seccomp_data` 的 16 个对齐 32 位字
    （`nr`、`arch`、`instruction_pointer` 低/高、6 个参数各自的低/高字）；
  - 每个值是“三态位域 `(knownMask, knownOnes)` + 无符号区间 `[lo,hi]`”的
    积域，所有传输函数都是可靠过近似；
  - 沿 DAG 对条件跳转双分支探索，记录路径约束
    （`eq/ne/ugt/uge/ult/ule`，`jset` 归约为 `(a&k)!=0`），不可行分支立即剪枝；
  - 反例具体化使用约束引导的**位级 DPLL**（区间/位掩码可行性剪枝 + 全常量时
    精确判定），按 `arg0.hi,arg0.lo,arg1.hi,...` 从高位到低位、0 优先分支，
    返回全序意义下的最小元组；节点/路径均有预算，预算耗尽会置
    `Report.Complete=false` 而不是武断报安全；
  - 每个输出反例都再经具体解释器运行并核对策略，**只报告可复现真反例**。
- **策略模型** `Policy`/`Rule`/`Arch`/`ArgCond`：
  - 架构：`x86_64`(`0xC000003E`)、`i386`(`0x40000003`)、
    `aarch64`(`0xC00000B7`)、`x32`（`arch=AUDIT_ARCH_X86_64` 且
    `nr |= 0x40000000`）；
  - `ArgCond{Index,Mask,Value}` 表达 `(arg & Mask) == Value`，多个条件 AND；
    `Mask=^uint64(0)` 即精确相等。
- **宽松动作**：默认仅 `SECCOMP_RET_ALLOW`；`WithLogAsLax()` 把 `LOG`
  （记录后允许）也算作绕过；`WithLaxActions(...)` 可加入 `TRACE` 等。
- **多层叠加** `VerifyStack`：对共享输入联合探索各层路径，按合并函数判定。
  - `MergeNumeric` / `MinReturns`：题面要求的“32 位返回值数值最小者胜”；
  - `KernelMerge` / `WithKernelMerge()`：精确的内核规则
    `(cur & SECCOMP_RET_ACTION) < (ret & SECCOMP_RET_ACTION)`，掩码
    `0x7fff0000` 忽略 `DATA` 与 bit31，同优先级先见者（最新安装的过滤器）胜。
- **交叉核验** `ExhaustiveCrossCheck` / `Enumerate`：在显式给出的小参数域上
  全枚举具体执行，与符号结论比对；另含固定随机种子的差分测试。

## 三类经典绕过如何被发现

- **架构混淆**：过滤器只比较 `nr == 59` 而不校验 `arch`。策略同时绑定
  `i386/11`、`aarch64/221` 后，最小反例为
  `(i386=0x40000003, nr=11, args=0)`，过滤器走到 `ALLOW`。
- **x32 bit 30**：过滤器校验了 `arch==AUDIT_ARCH_X86_64`，但直接比较原始
  `nr`。`x32` 的 `execve` 号是 `59|0x40000000 = 0x4000003B`，不等于 59，
  于是放行。最小反例 `(x32, 0x4000003B, ...)`。正确修法是比较前
  `nr & 0xBFFFFFFF`（或 `& ~0x40000000`），测试 `TestX32MaskFixIsSafe`
  验证修复后安全。
- **64 位参数高 32 位漏检**：策略要求 `arg1` 高字为 `0xDEADBEEF`，而过滤器
  只 `LD` 了 `arg1` 的低字。求解器保留高字规格约束、把无关低字取 0，得到
  最小反例 `arg1 = 0xDEADBEEF00000000`，具体运行为 `ALLOW`。若 `ALLOW`
  路径还要求某低位置位，DPLL 会强制该位，例如
  `arg1 = 0xDEADBEEF80000000`。

## 关于 `SECCOMP_RET_KILL_PROCESS` 与“数值最小”

`SECCOMP_RET_KILL_PROCESS = 0x80000000`，作为无符号 32 位整数它**大于**
`SECCOMP_RET_ALLOW = 0x7FFF0000`。因此按题面字面的“原始返回值数值最小”规则，
`min(KILL_PROCESS, ALLOW) == ALLOW`（见 `TestStackNumericKillProcessQuirk`）。
真实内核用 `SECCOMP_RET_ACTION (0x7FFF0000)` 掩码后再比较，使
`KILL_PROCESS` 与 `KILL_THREAD` 同属最高优先级。审计真实内核行为时请使用
`WithKernelMerge()`；需要严格复现题面数值规则时使用默认的 `MergeNumeric`。

## 测试

```sh
go test ./...            # 全部单元/示例/差分测试
go test -race ./...      # 竞态与重复运行
go test -short ./...     # 跳过多种子大规模差分
```

测试集包括：8 字节黄金解码与全操作码往返、静态校验各失败面、具体模拟器语义、
三类真实绕过、严格安全过滤器、两种合并规则与叠加栈、小参数域全枚举交叉核验，
以及 160+1200 组固定种子随机差分（枚举发现绕过 ⇒ 符号必发现；符号反例必可具体复现）。

## 主要导出 API

| 概念 | 符号 |
| --- | --- |
| 指令 / 编解码 | `Instruction`(`SockFilter`)、`Decode`、`DecodeOne`、`EncodeAll`、`Disassemble` |
| 静态校验 | `Validate`、`ValidateWith`、`ValidateStrict`、`ValidationError` |
| 具体执行 | `Run`、`RunTuple`、`Data`、`Tuple` |
| 策略 | `Policy`(`Spec`)、`Rule`、`Deny`、`NewPolicy`、`Arch`、`ArgCond` |
| 符号验证 | `Verify`、`VerifyWith`、`VerifyBytes`、`Verifier`、`Report`、`Counterexample` |
| 选项 | `WithLaxActions`、`WithLogAsLax`、`WithPathBudget`、`WithSolverBudget`、`WithKernelMerge` |
| 多层合并 | `VerifyStack`、`MergeNumeric`/`MinReturns`、`KernelMerge`/`MergeKernel` |
| 枚举核验 | `Enumerate`、`ExhaustiveCrossCheck`、`EnumDomain` |

## 范围与设计说明

- 这是一个**审计/建模**包，不调用 seccomp 系统调用，也不加载过滤器到内核。
- `BPF_LEN` 在内核 seccomp 上下文没有 `skb->len` 对应物，模型取常量 0；
  非常量 `BPF_IND` 地址被可靠过近似为自由变量，最终仍由具体执行复核。
- 符号域是过近似，因此可能产生需要回退求解的路径，但所有对外报告的反例都经过
  具体解释器验证，不存在误报；路径/求解预算保证分析必然终止，预算耗尽通过
  `Complete=false` 显式告知“不能证明安全”。
