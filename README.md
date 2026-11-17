# 外设命令描述符权限

低信任插件提交的设备操作包含读、写和间接地址，顶层范围检查可能漏掉间接目标。`descriptor`
包在设备代理提交命令前做完整的两阶段验证：

1. 用不可变页快照递归解析顶层表和所有间接表，只接受调用方声明的有限指令模式。
2. 所有可达写地址都经调用方授权策略检查；共享子表只解析一次但按引用展开操作。
3. 只有完整验证成功才返回 `Plan`。`Plan.Submit` 先冻结全部引用页，再以单次调用提交完整操作列表。

任何未知操作码、非法尾项、越权写、间接环或越界都会使验证失败，不返回计划，因此不存在“前缀已执行、尾部失败”的部分执行。

## 使用

```go
pages := myPageStore{}                 // PageSize + ReadPage
auth, _ := descriptor.NewRegionSet(
    descriptor.Region{Start: 0x8000, Size: 0x1000, Allow: descriptor.AccessWrite},
)

format := &descriptor.Format{
    EntrySize: 16,
    Opcode:    descriptor.OpcodeField{BitOffset: 0, BitWidth: 8},
    Patterns: []descriptor.Pattern{
        {
            Name:  "write",
            Value: 1,
            Fields: []descriptor.AddressField{{
                Name:       "dst",
                BitOffset:  8,
                BitWidth:   56,
                Kind:       descriptor.FieldWrite,
                AccessSize: 8,
                // raw 页号字段 -> 字节地址
                Semantics: descriptor.AddressSemantics{Scale: 4096},
            }},
        },
        {
            Name:  "next",
            Value: 2,
            Fields: []descriptor.AddressField{{
                Name:      "table",
                BitOffset: 8,
                BitWidth:  56,
                Kind:      descriptor.FieldIndirect,
                Table:     indirectTable,
            }},
        },
    },
}

v := descriptor.New(pages, auth)
v.CheckReads = true // 同时要求直接读和间接表内存的读权限
plan, err := v.Validate(descriptor.Request{
    Address: 0,
    Length:  rootBytes,
    Format:  format,
})
if err != nil {
    return err // 没有计划，也没有任何设备操作
}

if err := plan.Submit(freezer, submitter); err != nil {
    return err
}
```

`Submitter.Submit` 实现必须把操作列表当作一个事务；验证器保证校验失败时不会调用它，且调用前所有引用页已经冻结。`CheckReads` 默认关闭时仅强制检查写权限；打开后会检查直接读及间接表本身。

## 测试

```sh
go test -race ./...
```

测试包含自修改表、两个根项共享同一子表、根/子表非法尾项、间接写越权、间接环以及冻结失败时不提交。
