# layout_checker

结构布局检查器（C++17，头文件库，无外部依赖）。

根据字段大小、对齐、数组长度与显式填充描述，独立计算二进制布局
（不依赖当前编译器），并比较新旧两版布局，报告**首个**偏移、大小或
对齐变化。支持嵌套结构与联合体；所有算术均做 64 位整数溢出检查。

## 构建与测试

```sh
make check
```

## 用法

```cpp
#include "layout_checker.hpp"
using namespace layoutcheck;

// 旧版: struct { char a; int b; }
auto oldS = structOf({field("a", primitive(1, 1)),
                      field("b", primitive(4, 4))});
// 新版: struct { char a; int x; int b; }
auto newS = structOf({field("a", primitive(1, 1)),
                      field("x", primitive(4, 4)),
                      field("b", primitive(4, 4))});

if (auto d = compareLayouts(*oldS, *newS)) {
    // d->kind == Diff::Kind::Offset, d->path == "<root>.b"
    // d->message == "offset of '<root>.b' changed: 4 -> 8"
}
```

## API 概览

- 类型构造：`primitive(size, align)`、`arrayOf(elem, count)`、
  `structOf({...})`、`unionOf({...})`、`field(name, type)`、
  `padField(name, bytes)`（显式填充）。
- `computeLayout(type)` → `std::optional<Layout>`，含 `size`、`align`
  与每个成员的偏移；溢出或描述非法时返回 `std::nullopt`。
- `compareLayouts(oldT, newT)` → `std::optional<Diff>`，首个差异，
  `Diff::Kind` 涵盖 `Offset` / `Size` / `Align` / `Count` /
  `KindMismatch` / `FieldRemoved` / `FieldAdded` / `Overflow`。

## 语义说明

- 结构体大小向上取整到最大对齐（尾部填充）；数组成员按名字匹配，
  因此字段重排、增删也能正确定位。
- 联合体所有成员偏移为 0，大小取最大成员并对齐。
- 内层结构在自身尾部填充内增长时，外层布局不变（尾部填充复用），
  检查器只报告内层差异，不误报外层偏移。
