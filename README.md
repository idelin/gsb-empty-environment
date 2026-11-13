# 解优化重建时遗失的对象身份

脚本引擎解优化时把优化帧重建成解释帧，两个指向同一对象的值若被物化两次，随后释放与读取可能发生在不同身份上。给Go引擎测试宿主写可导入的重建包：输入寄存器、栈槽与至多64个虚拟对象定义，字段只能是整数、已存在堆对象ID或虚拟对象引用，允许共享和循环；重建描述把解释帧槽位映射到这些值。先为每个可达虚拟ID分配唯一对象，再填字段，已有堆对象不得复制。分配器可在任一次分配失败，此时回收本次所有新对象且不得发布半帧，也不得释放原堆对象；所有新对象字段填充不会调用外部代码；已有对象字段不修改，成功后输出每个虚拟ID与实际对象ID的对应证明。攻击回归用共享引用分别执行关闭与读取，每个虚拟对象携closed布尔字段，关闭将其置真，读取closed对象必须拒绝；关闭标记须经所有别名可见；另测自环、无根对象、重复定义、缺失引用与逐点分配失败，不能仅比较重建后的字段值相等。

## 实现

`Reconstruct` 严格分为两个阶段：

1. 校验完整描述、从寄存器与栈槽计算可达虚拟 ID，并为每个可达 ID 至多分配一次对象。
2. 只对已缓存的新对象指针填充字段，并把帧槽位中的虚拟引用改写成实际对象 ID。该阶段不调用分配器或任何宿主回调。

共享引用和循环因此自然得到同一身份：`Proof` 中一个虚拟 ID 只对应一个实际对象 ID，`Frame.Registers` / `Frame.Stack` 中的所有别名都指向该 ID。无根定义不会被分配，但仍会校验以拒绝悬空元数据。重复定义、缺失虚拟引用、非法值类型和超过 64 个定义都会在分配前失败。

分配失败是事务性的：已分配的新对象按逆序释放，`Reconstruct` 返回 `(nil, nil, err)`，已有堆对象既不释放也不修改。`Heap` 的 `Release` 还会再次拒绝释放任何 `AddExisting` 注册的对象。

## 用法

```go
in := netsec.Reconstruction{
    Registers: []netsec.Value{netsec.VirtualRef(7)},
    Stack:     []netsec.Value{netsec.VirtualRef(7)},
    Objects: []netsec.VirtualObject{{
        ID: 7,
        Fields: map[string]netsec.Value{
            "value": netsec.IntValue(42),
            "self":  netsec.VirtualRef(7),
        },
    }},
}

frame, proof, err := netsec.Reconstruct(in, netsec.NewHeap())
if err != nil {
    return err
}
if proof[7] != frame.Registers[0].Heap {
    return errors.New("shared virtual object lost identity")
}
```

`Heap` 是测试宿主；引擎也可以传入自己的分配器。支持 `Allocate/Release` 或 `Alloc/Free`，返回值可以是 `*Object` 或数值对象 ID。数值模式下新对象的 `Object` 记录由本包创建并通过 `Frame.Objects` 暴露。

## 攻击回归

运行：

```sh
go test ./...
go test -race ./...
```

`attack_regression_test.go` 通过共享寄存器/栈槽别名关闭对象，再从另一别名读取，并断言实际对象 ID 与指针相同。它同时验证“字段值相等”不能替代身份相等。其余测试覆盖自环、互环、无根对象、重复定义、缺失引用、64 个对象边界，以及第 1..N 次分配逐点失败后的回滚。
