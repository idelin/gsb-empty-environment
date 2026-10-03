# peritext: 富文本格式标注区间合并器 (Peritext/Fugue 风格 CRDT)

Go 标准库实现的富文本重叠格式标注（粗体、斜体、下划线、超链接等）CRDT 合并器。

## 模型

- **文本序列 (Fugue-lite)**: 每个字符是树节点，记录 insert-after 父指针；
  同一父节点下的兄弟按字符 ID（Seq 降序、Actor 降序）排序，并发插入在所有副本上确定性收敛。
- **格式区间 (Peritext)**: 格式标记是锚定在字符 ID 上的操作（而非数字偏移），
  并发编辑不会破坏区间边界；区间内部的并发插入自动继承格式。
- **冲突的边界因果判定**:
  - `remove` 只取消它**因果上看到过**的 `add`（记录在 `RemoveMark.Cancels` 中）。
    并发的"加粗 vs 取消加粗"互不影响，加粗确定性保留；先看到再取消，取消生效。
  - 同一属性多个并发 `add`（如两个不同的超链接）按 (Lamport 时间戳, Actor) 确定性裁决。
- **合并**: 状态是操作的 grow-only 并集，`Merge` 满足结合律、交换律、幂等。

## 运行

```sh
go test ./...      # 测试
go run ./cmd/demo  # 演示: 验收场景 + 冲突判定 + 链接冲突
```

## 验收场景

文本 `ABC`：用户1 给 `AB` 加粗，用户2 并发给 `BC` 加下划线。合并后：
`A`=bold，`B`=bold+underline，`C`=underline，三个连续区间无格式断裂，
双向合并结果一致（见 `TestAcceptanceOverlappingFormats`）。
