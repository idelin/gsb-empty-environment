# SGM 视差聚合库 (C++17)

接收逐像素逐视差整数代价体，沿 4 个方向（左→右、右→左、上→下、下→上）做
带小跳惩罚 `P1` 与大跳惩罚 `P2` 的动态规划（SGM），聚合后按 WTA 选视差。

## 核心设计

- **动态规划递推**（`src/sgm.cpp` 中 `scan_direction`）：
  `L_r(p,d) = C(p,d) + min(L_r(p-r,d), L_r(p-r,d±1)+P1, min_k L_r(p-r,k)+P2) - min_k L_r(p-r,k)`
- **边界重启**：前驱 `p-r` 越界时 `L_r(p,d) = C(p,d)`，路径在图像边界重新开始。
- **代价归一化**：每步减去前驱路径最小值 `min_k L_r(p-r,k)`，本地代价 `C`
  在每像素每方向只累计一次，路径代价有界、不溢出。
- **并列视差固定规则**：WTA 用严格小于比较，并列时固定取最小视差。

## 构建与运行

```sh
cmake -S . -B build && cmake --build build
./build/sgm_demo   # 或 ctest --test-dir build
```

## 接口

```cpp
sgm::Aggregator agg(width, height, num_disparities, sgm::Params{p1, p2});
std::vector<int> disp = agg.compute(costs);   // costs[(y*w+x)*nd + d]
agg.direction_costs(dir);                     // 逐方向聚合代价 L_r
agg.direction_disparities(dir);               // 逐方向 WTA 视差(对照用)
agg.aggregated_costs();                       // 四方向求和 S(p,d)
```

demo 输出小图（12x6）的真值、4 个方向各自的 WTA 视差图、四方向聚合后的
最终视差图，以及抽样像素的逐方向代价对照表和并列视差选择演示。
