# ediff — 灰度误差扩散量化库（C++17）

逐行往返（蛇形）扫描的误差扩散量化器，全程整数精确累计，无任何浮点运算。

## 规则定义

- **扫描顺序**：第 0 行按 `Options::start` 方向扫描，每行结束换向（蛇形）。
- **误差扩散**：量化误差按有理权重 `num/den`（`Kernel::taps`）分配给尚未处理的
  像素；反向（右→左）扫描的行，权重自动水平镜像。权重只许指向未处理像素
  （`dy > 0`，或 `dy == 0 且 dx > 0`）。
- **精确累计**：每个像素的累计误差以 int64 分子 + 公共分母 `den` 保存，
  因此结果与分块方式、处理顺序的内部实现无关。
- **最终取整**：对精确累计值取最近灰度级；恰好位于两级中点时取较高级
  （round-half-up），见 `nearestLevel`。
- **边界丢弃**：指向图像外的误差份额被丢弃，其总和以精确有理数
  `Result::discarded_num / discarded_den` 单独计量（可为负）。

## 已验证性质（tests/test_quantize.cpp）

- 取整规则与中点 tie 行为；手算单像素、单行、2x2 蛇形算例。
- **镜像 ↔ 反向扫描**：`quantize(mirror(img), 正向起始)` 与
  `mirror(quantize(img, 反向起始))` 输出逐像素一致，丢弃误差相等（反向同理）。
- **分块不变性**：`quantizeTiled` 以任意行高（1, 2, 3, …, 整图）分块处理，
  输出与丢弃误差和一次性 `quantize` 完全一致。
- 行缓冲实现与独立的整图网格参考实现在随机图像、两种起始方向、
  自定义权重（Jarvis 风格）与多级调色板下完全一致。

## 构建与测试

```sh
make test
```

## 使用示例

```cpp
#include "ediff/quantize.h"

ediff::Image img(w, h, pixels);          // pixels: 0..255，行优先
ediff::Options opt;                      // 默认 {0,255} + Floyd–Steinberg 7/16,3/16,5/16,1/16
ediff::Result r = ediff::quantize(img, opt);
// r.out: 量化结果；r.discarded_num / r.discarded_den: 边界丢弃误差总和
```
