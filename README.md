# typeset

面向二进制防护宿主的 Go 类型集合分析包。输入 C++ 风格类继承图、对象分配点、向上转型、字段赋值和虚调用点，输出每个调用点的精确候选目标、分配来源路径和歧义报告。

## 语义

- 继承图必须无环；发现环时返回 `ErrInheritanceCycle`（`*CycleError`）。
- 对象布局按完整子对象路径物化。非虚重复基类通过路径中的边索引区分，例如 `B[0]::A` 与 `B[1]::A`。
- 虚基类在整个最派生对象中只有一个共享子对象；虚基类内部继承的非虚子对象也随该虚基类共享。
- 虚调用先找到引入虚槽的根类，再按动态类型计算最终覆盖者。共享虚槽存在多个互不为祖先的最终覆盖者时，返回全部候选并把 `Ambiguous` 置位，同时记录 `Ambiguity`。
- 方法默认按虚方法处理；可用 `Method.NonVirtual` / `Method.Static` 或 `Method.Kind = "nonvirtual"` 标记静态绑定方法。模型一旦显式使用虚/非虚标记，未标记方法按非虚处理；但同一覆盖链中只要出现过显式虚声明，未标记的覆盖声明仍按虚方法处理。
- 分配、向上转型和字段赋值按有限集合做不动点传播。结果中的 `Provenance` 同时给出分配点、接收者子对象路径、向上转型与字段赋值轨迹。
- 每个调用点还会经过有限对象执行模型复核；若候选集合漏掉真实目标，返回 `ErrValidationFailed`。

## 快速使用

```go
model := typeset.Model{
    Classes: []typeset.Class{
        {Name: "A", Methods: []typeset.Method{{Name: "f"}}},
        {Name: "B", Bases: []typeset.Base{{Class: "A"}}, Methods: []typeset.Method{{Name: "f"}}},
        {Name: "C", Bases: []typeset.Base{{Class: "B"}}, Methods: []typeset.Method{{Name: "f"}}},
    },
    Allocations: []typeset.Allocation{{ID: "obj", Class: "C", Site: "new C"}},
    Calls: []typeset.VirtualCall{{
        ID: "call-1", Receiver: "obj", StaticType: "A", Method: "f",
    }},
}

result, err := typeset.AnalyzeModel(model)
if err != nil {
    // 继承环、非法模型或有限模型校验失败
}

for _, call := range result.Calls {
    for _, target := range call.Targets {
        fmt.Println(target.Key()) // C::f
    }
    for _, p := range call.Provenance {
        fmt.Printf("alloc=%s site=%s path=%v\n", p.Allocation, p.AllocationSite, p.Path)
    }
}
```

也支持宿主直接传切片：

```go
result, err := typeset.Analyze(classes, allocations, upcasts, assignments, calls)
```

需要把歧义作为错误处理时使用：

```go
result, err := typeset.AnalyzeStrict(model)
if errors.Is(err, typeset.ErrAmbiguousVirtualSlot) {
    // result 仍包含全部候选与歧义细节
}
```

## 输入字段

`Class`：

- `Bases` / `BaseClasses`：直接基类，`Virtual` 表示共享虚基类边。
- `Methods`：`Name` 或 `Signature` 是方法身份；默认虚方法。
- `Fields`：可选的数据成员声明。未声明字段时，字段槽以当前静态子对象路径作为作用域。

`Allocation`：

- `ID`、`Class`、`Site`：分配点标识、动态类型和来源位置。
- `Fields`：可选初始字段值，值为分配/转型/字段表达式 ID。

`Upcast`：

- `From`（或 `Object` / `Source` / `Value`）到 `To`（或 `Target` / `Type`）。
- 非虚重复基类产生多个目标子对象时，用 `Path` / `PathString` 选择其中一个，例如 `["B[1]", "A"]`。
- 没有显式选择路径时会返回所有可能路径并报告 `conversion` 歧义。

`FieldAssignment`：

- `Object`（或 `Receiver` / `Target`）的 `Field` 被赋值为 `Value`（或 `Source` / `From`）。
- `Class` 可表示“该静态类的所有对象”；`Owner` 可显式指定字段声明类。
- 字段落在重复非虚基类中时，可用 `Path` / `PathString` 选择子对象槽。

`VirtualCall`：

- `Receiver` 可为分配 ID、转型 ID、类名或 `obj.field` 表达式；也可用 `Object` + `Field`。
- `StaticType` 是调用点的声明类型；需要时自动做隐式向上转型。
- `Path` / `PathString` 可指定重复基类中的子对象。

## 结果

- `CallResult.Targets`：候选目标集合（按 `class::method` 去重排序）。
- `CallResult.Provenance`：每个目标对应的分配来源和子对象路径。
- `CallResult.Ambiguities`：共享虚槽、调用查找、转换或字段查找歧义。
- `Result.Layouts`：每个动态类型物化后的完整对象布局。
- `Result.Validation`：有限对象执行模型的期望、缺失和多余目标。

JSON 输入可直接使用：

```go
result, err := typeset.AnalyzeJSON(data)
```
