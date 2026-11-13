# 安全栈分类漏掉间接取址变量

编译器把局部变量分到安全栈与不安全栈，若别名分析漏掉间接取址，缓冲区溢出会覆盖返回元数据。本模块给 Go 编译验证宿主提供一个可导入的栈分类包：输入有限 SSA 图，局部对象有大小、对齐和用途，指针操作含 AddrOf、GEP 常量/有界变量、Phi、Select、StorePtr、LoadPtr、PassCall、PtrToInt、IntToPtr 和 ReturnPtr；调用摘要逐参数声明 NoCapture、ReadOnly 及可访问最大偏移，未知调用摘要按可捕获且任意访问。对象只有在所有派生指针都不逃逸、所有访问范围可证明在对象内、没有 PtrToInt/IntToPtr 来源、且不与变长对象共享布局时才可放安全栈；否则进不安全栈。返回地址、影子栈指针等控制元数据不由本包布局。分析别名按对象与常量字段路径区分，未知索引合并为整对象；分析字段敏感，递归调用摘要求不动点，预算耗尽保守判不安全。另接受待审分类回调，搜索被其标安全但参考模型不安全的最短来源路径，并用布局模拟器把越界写扩展到相邻槽，只有能触及控制元数据或另一个安全对象才报告安全影响。覆盖 Phi 一支逃逸、NoCapture 但越界摘要、指针存入局部再取出、整数往返及安全只读切片。

## 快速使用

```go
g := netsec.Graph{
    Objects: []netsec.Object{{
        ID: "slice", Size: 64, Align: 8, Purpose: netsec.PurposeSliceBacking,
    }},
    Values: []netsec.Value{
        {ID: "p", Kind: netsec.OpAddrOf, Object: "slice"},
        {ID: "p8", Kind: netsec.OpGEP, Base: "p", Offset: 8},
        {
            ID: "read", Kind: netsec.OpPassCall, Callee: "reader",
            Args: []netsec.ValueID{"p8"},
            Summary: &netsec.CallSummary{Params: []netsec.ParamSummary{{
                Index: 0, NoCapture: true, ReadOnly: true, MaxOffset: 7,
            }}},
        },
    },
}

r := netsec.Analyze(g)
fmt.Println(r.ClassOf("slice")) // safe
```

`PassCall.Summary` 也可以用 `Graph.Summaries` 按被调函数声明。摘要缺失、`Unknown` 或负数 `MaxOffset` 都按“可捕获且任意访问”处理。若提供 `Graph.Functions`，缺省会对函数体做递归摘要不动点推断；也可显式调用 `Summarize`。

`Audit` 的待审回调接受 `func(ObjectID, Object) StackClass`，也兼容一参回调、`bool`/`string` 返回值及 `map[ObjectID]StackClass`：

```go
a := netsec.Audit(g, func(id netsec.ObjectID, _ netsec.Object) netsec.StackClass {
    return netsec.ClassSafe
}, netsec.WithLayout(layout))
for _, finding := range a.FalseSafe {
    fmt.Println(finding.Object, finding.Path) // 对象到不安全汇点的最短路径
}
```

布局槽位由宿主提供；`LayoutSlot{Kind: SlotControl}` 代表已由宿主定位的返回地址或影子栈等元数据，本包不会为它分配偏移。`SimulateLayout`（或 `Audit` 配合 `WithLayout`）只返回触及控制槽或另一个参考模型安全对象的越界写影响。
