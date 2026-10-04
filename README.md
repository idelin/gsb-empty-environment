# grayrecon

C++17 灰度形态学重建库：对标记图（marker）反复做邻域膨胀并用上界掩码（mask）截断，
直到收敛（reconstruction by dilation）。

## 算法

采用 Vincent 混合算法，避免整图反复扫描：

1. **校验**：要求 marker 逐像素 ≤ mask，否则抛出 `MarkerExceedsMaskError`。
2. **初始化**：一次正向 + 一次反向光栅扫描，沿已访问邻居传播并截断。
3. **传播**：FIFO 队列只访问仍可能变化的像素，直到队列为空（收敛）。

## 接口

```cpp
#include <grayrecon/reconstruct.hpp>

grayrecon::Image marker(w, h), mask(w, h);   // 8-bit 灰度，行主序
auto res = grayrecon::reconstructByDilation(marker, mask,
                                            grayrecon::Connectivity::Eight);
res.image;       // 重建结果
res.updates;     // 像素值更新次数
res.queuePops;   // 队列传播次数
res.fixedPoint;  // 固定点校验：min(dilate(result), mask) == result
```

支持 4/8 连通（`Connectivity::Four` / `Eight`）。`isFixedPoint()` 可单独做固定点校验。

## 构建与测试

```sh
cmake -B build -S . && cmake --build build
ctest --test-dir build        # 或直接运行 ./build/test_reconstruct
```

测试覆盖：狭长蜿蜒通道、大面积相同灰度平坦区域、灰度屏障、
非法输入拒绝（marker > mask、尺寸不符），以及随机图像与朴素全图扫描参考实现的交叉验证。
