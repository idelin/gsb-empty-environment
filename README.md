# locator

错误展示用的坐标定位库：把字节偏移转换为 1-based 行号、Unicode 标量列和
UTF-16 码元列，并支持反向查询。行索引可随编辑增量更新。

## 特性

- 行终止符：LF、CRLF、独立 CR；CRLF 跨两次编辑（如 `Append("...\r")` 后
  `Append("\n...")`）仍算一个终止符。
- 非法 UTF-8：每个坏字节计为一个 U+FFFD 替换字符（与 `utf8.DecodeRune`
  一致），坏字节偏移被记录，可经 `InvalidOffsets` / `Position.Replaced` 查询。
- 增量编辑：`Append` / `Insert` / `Delete` 只重扫受影响行到缓冲区末尾。
- 坐标往返：`Offset`（标量列）与 `OffsetUTF16`（UTF-16 列）。

## 用法

```go
ix := locator.FromString("hello\r\nwörld→🙂\nbad:\xffxfe")
pos := ix.Locate(20)        // Position{Line, Column, ColumnUTF16, Replaced}
off, ok := ix.Offset(2, 3)  // 反向：行+标量列 -> 字节偏移
ix.Insert(ix.Len(), []byte("\nmore"))
```

## 语义约定

- 行号、列均为 1-based；`LineCount` 对末尾终止符保留一个最后的空行
  （`"a\n"` 为 2 行）。
- 终止符字节归属其所结束的行，无逆坐标；往返保证适用于行内容范围内的偏移。
- 偏移落在多字节 rune 中间时，`Locate` 返回该 rune 首字节的坐标；越界列
  在反向查询中钳制到行尾并返回 `ok=false`。

## 测试

```sh
go test ./...
```
