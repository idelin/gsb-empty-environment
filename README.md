# graydiff — 精确灰度误差扩散量化库（C++17）

逐行往返（serpentine）扫描的灰度量化库：按给定有理权重把每个像素的量化误差
分配给尚未处理的像素，全程**精确整数累计**（无浮点、无中间舍入），只在最终
量化时按明确规则取整；落出图像边界的误差份额**单独计量**。

纯头文件库，无第三方依赖（自带最小大整数），C++17。

## 算法定义

- **扫描顺序**：第 0 行按 `startDirection`（默认左→右），之后每行反向，逐行往返。
- **核（有理权重）**：`Kernel{taps, den}`，每个 `Tap{dx, dy, num}` 表示权重
  `num/den`，坐标以扫描前进方向为基准（`dy>0` 为下方的行；`dy==0` 时要求
  `dx>0`，即只指向未处理像素）。反向行处理时 `dx` 自动取反，核只需描述一次。
  预置：Floyd–Steinberg、Jarvis–Judice–Ninke、Stucki、Atkinson、rightHalf。
- **精确累计**：累积值的有理分母在数学上会沿扫描顺序增长（den^k），因此引擎
  采用全局公分母 `D = den^(W·H)·(levels−1)`，所有累积量都是整数（单位 1/D）。
  可证明第 r 个像素的误差必含因子 `den^(N−r)`（N=W·H），故分配时 `E/den`
  恒为精确整除（引擎内部断言此不变量）。全程无任何舍入。
- **最终取整规则**：只在量化时发生一次——round-half-up：
  `k = #{ j∈[1,levels−1] : 2·(levels−1)·A ≥ (2j−1)·maxValue·D }`，
  即恰好落在两级正中间时取较高一级；越界自动钳制到 `[0, levels−1]`。
- **边界丢弃**：落出图像的分配份额累加为 `discardedNum / denominator`，
  由 `Result` 单独返回。权重和为 1（`Σnum == den`）时守恒恒等式成立：
  `Σ误差 = Σ后续像素收到的贡献 + 边界丢弃`（测试逐位验证）。

## 关键性质（均有测试）

- **镜像 ↔ 反向扫描**：`quantize(flipH(I), startDirection=R→L)` 的输出等于
  `flipH(quantize(I, startDirection=L→R))`，且边界丢弃误差完全相等。
  注意必须同时翻转起始扫描方向——这是"镜像输入对应反向扫描"的含义。
- **分块不变**：`quantizeTiled(img, opt, tileW, tileH)` 按条带+行段切分同一
  条 serpentine 像素流，任意 `(tileW, tileH)`（含 1×1、整行、整幅、超出图幅）
  的输出与边界丢弃计量都与整幅处理逐位相同。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
ctest --test-dir build          # 或 ./build/graydiff_tests
./build/graydiff_demo 256 160 2 out.pgm   # 生成 Floyd–Steinberg 二值抖动 PGM
```

## 用法

```cpp
#include <graydiff/quantize.hpp>
using namespace graydiff;

Image img(W, H);                 // pixels: 行优先, 取值 [0, maxValue]
Options opt;
opt.kernel = Kernel::floydSteinberg();
opt.levels = 2;                  // 量化级数 [2,256]
opt.maxValue = 255;
opt.startDirection = ScanDirection::LeftToRight;

Result r = quantize(img, opt);            // 或 quantizeTiled(img, opt, tw, th)
// r.levelIndex[y*W+x]  —— 量化级 [0, levels-1]
// r.discardedNum / r.denominator —— 边界丢弃误差（精确有理数）
// r.discardedErrorApprox()       —— 其近似浮点值（仅显示用）
// r.gray8Of(level)               —— 显示用 8 位灰度（round-half-up）
```

流式用法（自定义扫描/分块驱动）：构造 `Engine(W, H, opt)`，按 serpentine
顺序逐个 `feedPixel(v)`（返回量化级），最后 `finish()`。

## 复杂度说明

精确性的代价：公分母 `D` 有 `O(W·H·log den)` 位，每个像素的处理代价与累积
整数的长度成正比，整体约为 `O((W·H)²·log den / 32)` 次机器字运算，滚动缓冲
占用 `(maxDy+1)·W` 个大整数。适合对正确性要求高的中小图幅场景，或作为
近似实现的精确参照。256×160 的 Floyd–Steinberg 二值化在普通机器上约数秒。

## 文件结构

- `include/graydiff/bigint.hpp` — 最小精确大整数（加减、比较、乘/除小整数）
- `include/graydiff/kernel.hpp` — 核定义、校验与预置核
- `include/graydiff/engine.hpp` — 流式 serpentine 引擎（精确累计 + 取整 + 丢弃计量）
- `include/graydiff/quantize.hpp` — 整幅 / 分块驱动
- `tests/test_main.cpp` — 已知答案、取整规则、守恒恒等式、镜像、分块不变等测试
- `examples/demo.cpp` — 合成图抖动并输出 PGM
