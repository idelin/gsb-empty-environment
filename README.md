# layout_check — 结构布局检查器

接收字段大小、对齐、数组长度和显式填充，**算法化**计算二进制布局
（不依赖当前编译器的 `offsetof`/`sizeof`），并比较新旧两份布局，
按声明顺序报告**首个**偏移、大小或对齐变化。

## 构建与测试

```sh
make            # 构建 layout_check 和 tests
make check      # 运行单元测试
```

## 用法

```sh
./layout_check old.spec new.spec   # 比较两份布局；无差异退出码 0，有差异 1，错误 2
./layout_check --dump old.spec     # 打印计算出的布局
```

## 规格 DSL

```
struct Packet {
  field len   size=4 align=4              # 标量字段
  field data  size=1 align=1 count=16     # 数组（count = 元素个数）
  pad 3                                   # 显式填充字节
  struct Inner nonpod { ... }             # 嵌套结构；nonpod 允许尾部填充复用
  union  Payload { ... } [4]              # 联合体；[n] 表示该类型的数组
}
```

- 数字支持十进制与 `0x` 十六进制；`#` 为行注释。
- 对齐必须是 2 的幂，否则报错。

## 语义

- 结构体：成员按声明顺序放在 `align_up(cursor, align)` 处；
  `size = align_up(dsize, 最大成员对齐)`；显式 `pad` 占空间但不提升对齐。
- 联合体：所有成员偏移为 0；`size = align_up(最大成员 size, 最大对齐)`。
- **尾部填充复用**：每个类型同时计算 `size`（含尾部填充）与 `dsize`
  （不含尾部填充）。标记为 `nonpod` 的结构/联合体作为成员时，后续成员
  可从 `offset + dsize` 起放置，复用其尾部填充；`pod`（默认）则占满
  整个 `size`。
- **整数溢出**：所有偏移/大小运算均做 `uint64_t` 溢出检查，
  溢出时布局计算失败并报告具体成员，而不是回绕。

## 比较规则

`firstDiff` 按声明顺序逐成员比较：成员偏移 → 递归嵌套成员 → 成员大小
→ 成员对齐，最后比较整体 size / align / dsize，返回首个差异及点分路径
（如 `Packet.Inner.kind`）。成员数量不一致也会报告。

## 文件

- `layout.hpp` / `layout.cpp` — 布局引擎与 diff（库，可独立使用）
- `main.cpp` — DSL 解析器与 CLI
- `tests.cpp` — 单元测试（嵌套、联合体、数组、显式填充、尾部填充复用、溢出）
- `examples/` — 示例规格
