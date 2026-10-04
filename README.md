# gray-reconstruction

C++17 灰度形态学重建库（膨胀型）：从标记图 `marker` 出发，反复做邻域极大
膨胀并用掩码 `mask` 作为上界截断，直到稳定（不动点）。

## 算法

Vincent 混合算法，无需整图反复扫描：

1. 正向光栅扫描：用前序邻居传播；
2. 反向光栅扫描：用后序邻居传播；
3. FIFO 队列：只处理仍可能提升邻居的像素，直至队列空。

## 接口

```cpp
#include "reconstruction.hpp"

morpho::GrayImage marker = ..., mask = ...;
auto res = morpho::reconstruct_by_dilation(marker, mask,
                                           morpho::Connectivity::Eight);
if (!res) { /* 输入非法：尺寸不符或存在 marker > mask */ }
else {
    res->image;         // 重建结果
    res->update_count;  // 像素值被提升的总次数
    res->fixed_point;   // 固定点校验：J == min(mask, 邻域极大膨胀(J))
}
```

输入不满足「标记处处不高于掩码」时返回 `std::nullopt` 拒绝处理。

## 构建与测试

```sh
cmake -B build && cmake --build build && ctest --test-dir build
# 或直接：
c++ -std=c++17 -O2 -I src src/reconstruction.cpp tests/test_reconstruction.cpp -o test && ./test
```

测试覆盖：狭长蜿蜒通道、大面积相同灰度平台、非法输入拒绝、4/8 连通性
差异、随机图与朴素迭代实现的对拍、空图与单像素边界情况。
