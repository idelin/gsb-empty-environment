# layout

Go 值类型内存布局库：给定字段大小与对齐，计算结构体 / 联合体 / 数组的
尺寸、对齐和每个字段的偏移。

## 规则

- **结构体**：对齐取所有字段对齐的最大值；每个字段放在满足自身对齐的
  下一个偏移；末尾按结构体对齐补齐（尾填充）。
- **联合体**：所有成员起点都是 0；尺寸为最大成员尺寸按最大成员对齐补齐。
- **数组**：步长 = 元素尺寸（已含元素尾填充）；零长数组尺寸为 0，
  但保留元素对齐，仍会影响后续字段的偏移。

## 检查

- 整数溢出：对齐补齐、偏移累加、数组 步长×个数 均做溢出检查并返回错误。
- 递归值类型：按值包含自身（直接或间接，经数组/联合体）返回错误；
  同一 `*Type` 被多个字段复用属于共享而非递归（结果按指针记忆化）。

## 用法

```go
s := &layout.Type{Kind: layout.Struct, Name: "S", Fields: []layout.Field{
    {Name: "a", Type: &layout.Type{Kind: layout.Prim, Size: 1, Align: 1}},
    {Name: "b", Type: &layout.Type{Kind: layout.Prim, Size: 8, Align: 8}},
}}
l, err := layout.Compute(s) // size=16 align=8, a@0, b@8
fmt.Println(l)              // 逐字段打印偏移
```

## 测试

`layout_test.go` 含逐字节占位模型（byte model）：把每个字段的每个字节
放进占用表，独立核对偏移对齐、结构体无重叠、联合体成员全在 0、
尾填充后尺寸一致，并覆盖零长数组与嵌套联合体场景。

```sh
go test ./...
```
