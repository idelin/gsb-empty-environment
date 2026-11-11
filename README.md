# wiretypes

网络消息解码器需要按协商模式限制可实例化类型。写Go对象解码包，按稳定类型ID、字段类型和引用表构造带环对象，先验证所有引用及资源预算，再交给业务回调；类型注册表热切换不能影响已开始的一条消息。测试前向引用、别名共享和拒绝对象的零副作用。

## 设计

- **稳定类型 ID**：`TypeID` 是线上协议中的固定标识；`Registry` 把 ID 绑定到无副作用的工厂和 `FieldSpec` 字段描述，注册时即完成结构校验（Go 字段存在性、种类匹配、引用类型可赋值、基类链无环）。
- **协商模式**：`Mode` 是允许实例化的类型 ID 白名单；表中任何对象的类型未注册或未协商都直接拒绝。
- **引用表与环**：`Message` 是 `RawObject` 引用表加根 ID。解码分两阶段——先 `new` 出全部实例，再按字段装配，因此前向引用、别名（同一 ID 多处引用共享同一实例）和任意环都能构造。
- **先验证后构造**：所有引用目标、类型兼容（含基类子类型）、必填/可空字段、种类匹配以及 `Budget`（对象数、字段数、引用数、嵌套深度）全部通过后才会调用工厂；失败用 `errors.Join` 聚合，错误可用 `errors.Is` 匹配 `ErrUnknownRef`、`ErrBudgetExceeded` 等哨兵。
- **事务式回调**：图完整装配后先对每个对象调用 `Callback.Check`，任一拒绝则整批不调用任何 `Receive`，并返回包装了 `ErrRejected` 的错误；通过后按引用表顺序 `Receive`。校验失败连工厂都不会触发，拒绝消息对外零副作用。
- **热切换隔离**：`Registry` 内部用 `atomic.Pointer` 持有不可变 `Snapshot`，每次 `Decode` 在入口绑定当前快照；`Swap` 只影响之后开始的消息，进行中的消息始终使用旧快照。`Swap` 入参非法时旧快照保持不变。

## 用法

```go
registry, _ := wiretypes.NewRegistry([]wiretypes.TypeSpec{
    {
        ID:  "person",
        New: func() any { return &Person{} },
        Fields: []wiretypes.FieldSpec{
            {Name: "Name", Kind: wiretypes.KindString},
            {Name: "Manager", Kind: wiretypes.KindRef, RefType: "person", Nullable: true},
        },
    },
})
decoder, _ := wiretypes.NewDecoder(
    registry,
    wiretypes.Mode{"person": true},
    wiretypes.Budget{MaxObjects: 64, MaxRefs: 256, MaxDepth: 16},
)
root, err := decoder.Decode(msg, wiretypes.FuncCallback{
    CheckFn:   func(obj any, t wiretypes.TypeID, id wiretypes.RefID) error { return nil },
    ReceiveFn: func(obj any, t wiretypes.TypeID, id wiretypes.RefID) { /* 入业务系统 */ },
})
```

## 测试

`go test ./... -race` 覆盖：前向引用与环、别名共享、可空值、协商拒绝、四类预算、各类校验失败、子类型引用、回调顺序与全图可见性、拒绝零副作用，以及热切换对在途消息无影响（含并发场景）。
