# 栈映射漏根让GC回收仍在用的对象

JIT在安全点提交活指针栈映射，漏掉寄存器或把整数误标指针会导致释放后访问或错误保活。给Go运行时测试宿主实现可导入的栈映射核验包：有限SSA函数含对象分配、指针复制、字段偏移、整数、Phi、Call、Safepoint和Return；每个值有Ptr(objectSet,offsetRange)或NonPtr类型，调用摘要声明哪些参数在调用期间保持活跃及返回来源。按控制流不动点计算每个Safepoint之后仍可能使用的指针值集合，异常边和循环都计入；对象内部指针的offsetRange必须完全落在对象大小内，且必须同时记录base对象身份和偏移，不能只保存裸地址。候选栈映射列寄存器/槽位到值ID及derived-base关系，必须覆盖全部活Ptr，不能包含NonPtr，重复位置冲突拒绝。另用有界具体解释器分配对象代次，在Safepoint按候选映射标记回收，再继续执行；任何后续Dereference已回收代次即输出最短路径，误标整数导致指向随机活对象也单列。覆盖Phi一边指针、调用异常、内部指针、同槽复用和循环背边，预算不足保守拒绝映射。

## 快速使用

```go
f := netsec.NewFunction("demo")
entry := f.NewBlock("entry")
obj := entry.Alloc("obj", 16, "alloc")
p := entry.Copy(obj, "root")
sp := entry.Safepoint("safe")
entry.Dereference(p, "use")
entry.Return(netsec.NoValue, "done")

maps := []netsec.StackMap{{
    At: sp,
    Entries: []netsec.StackMapEntry{{
        Location: netsec.Slot(0),
        Value:    p,
    }},
}}

if _, err := netsec.Analyze(f); err != nil { panic(err) } // liveness fixed point
report := netsec.CheckStackMaps(f, maps) // coverage/type/location checks
run := netsec.Execute(f, maps, netsec.DefaultRunConfig())
```

`ValueType` 的指针形态为 `KindPtr` + `Objects` + `Offset`，偏移范围是半开区间
`[Start, End)`。根指针通常使用 `[0,1)`；已知位于字段偏移 8 的指针使用
`[8,9)`。`Instr.Type.Base` 和 `StackMapEntry` 上的 `DerivedBase` 分别记录
静态来源值ID和运行时 base 值ID，二者都不能退化为裸地址。

`CallSummary.LiveArgs` 描述调用期间需要保活的参数，`Return` 支持 `RetNone`、
`RetNonPtr`、`RetArg` 和 `RetFresh`。`MayThrow` + `Exception` 会把异常控制流
边加入活跃性不动点。

## 校验结果

- `CheckStackMaps` 拒绝：缺少Safepoint映射、漏掉活Ptr、映射NonPtr、同一位置
  重复、未知值/位置、越界offset、缺失或错误的derived-base关系。
- `Execute` 在Safepoint按候选映射标记仍存活对象，然后回收其余对象代次。
  后续Dereference已回收代次会以 `UseAfterFree` 单列，并带最短执行路径。
- 候选值为整数、但整数值落入某个仍存活对象地址范围时，会以
  `MislabeledInteger` 单列，并记录被错误保活的站点与代次。
- `MaxSteps`、`MaxStates`、`MaxObjects`、`MaxAllocations` 任一耗尽时，
  结果状态为 `ExecRejected`，绝不会给出 `ExecSafe`。`ExecSafe` 只表示所有
  可达路径在有界执行中均无违例且映射本身通过校验。

测试覆盖漏根UAF、正确保活、内部指针、同槽复用、调用异常边、Phi单边指针、
循环背边、调用返回来源、重复位置、整数误标和预算拒绝。
