# 异常展开表达式沙箱

崩溃转储内的展开表达式不可信，计算栈帧位置不能读取任意宿主地址。写供Go展开宿主调用的栈式求值包，指令仅含常量、寄存器、加减、解引用和条件跳转，读取经授权内存回调，按指令步数与读取字节双计费；寄存器输入固定快照。用循环、地址回绕、短读与嵌套表达式验证失败不返回伪造完整结果。

## 用法

实现在 `unwind/` 包：

- `Decode(blob)` 把不可信字节流解码并校验为 `[]Instr`（拒绝未知操作码、截断操作数、越界跳转）。
- `Eval(prog, regs, reader, limits)` 返回 `(result, Stats, error)`；任何失败都返回零值结果与非 nil error，绝不伪造完整结果。
- 内存只经宿主实现的 `Reader.ReadAt` 授权窗口读取；短读（`n < 8`）视为致命错误。
- `Limits{MaxSteps, MaxReadBytes, MaxStack}` 双计费：指令步数与读取字节；零值 Limits 拒绝一切工作。
- 寄存器在求值入口拷贝为固定快照，求值期间调用方修改原切片不影响结果。

```go
prog, err := unwind.Decode(blob)
if err != nil { /* 拒绝该表达式 */ }
cfa, stats, err := unwind.Eval(prog, regs, authorizedMem, unwind.DefaultLimits())
if err != nil { /* cfa 为零值，不可使用；stats 仍可用于计费 */ }
```

运行测试：`go test ./...`
