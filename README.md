# ttfglyph

TrueType 字形轮廓读取器，直接面向渲染管线。

## 功能

- 通过 `io.ReaderAt` 按需读取：仅在实际访问时拉取所需的表
- 解析 sfnt 表目录、`cmap`（格式 4 与格式 12）、`loca`（短/长两种偏移）、
  `glyf`、`hmtx`、`head`、`hhea`、`maxp`
- 简单字形：完整解析 TrueType 标志位与单字节/双字节坐标增量编码
- 复合字形：支持 XY 偏移、点对齐、统一缩放、独立 XY 缩放与 2×2 仿射矩阵，
  递归展开嵌套组件
- 自动补出二次贝塞尔曲线中相邻两个 off-curve 点之间隐含的 on-curve 中点
- 安全上限：复合递归深度 16、单字形点数 16384、轮廓数 1024、总点数 65535
- 所有多字节读取都经过边界检查；损坏或截断的表返回 `ErrCorrupt`，
  不会发生越界访问

## 用法

```go
f, err := ttfglyph.New(r, size)          // r 实现 io.ReaderAt
gid, err := f.GlyphIndex('A')            // cmap 查字形索引
outline, err := f.GlyphOutline(gid)      // 轮廓（已补隐含中点）
metrics, err := f.HMetrics(gid)          // 水平度量
```

## 测试

测试用内存中构造的最小 TrueType 字体（简单字形、带缩放/矩阵/点对齐的复合
字形、嵌套复合、短 loca、cmap 格式 4/12）验证轮廓坐标，并覆盖截断、点数
上限、递归上限、损坏的 cmap/head/loca/hmtx 等错误路径。

```sh
go test ./...
```
