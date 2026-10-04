# sgm_aggregator

C++17 半全局匹配（SGM）视差聚合库。接收逐像素逐视差的整数代价体，
沿四个方向做带小跳（P1）/ 大跳（P2）惩罚的动态规划，聚合后按赢家通吃选视差。

## 算法

对每个方向 r ∈ {左→右, 右→左, 上→下, 下→上}：

```
L_r(p, d) = C(p, d) + min( L_r(p-r, d),
                           L_r(p-r, d-1) + P1,
                           L_r(p-r, d+1) + P1,
                           min_k L_r(p-r, k) + P2 )
            - min_k L_r(p-r, k)        # 代价归一化，数值不随路径长度膨胀
```

- **边界重启**：前驱 p-r 越界时 `L_r(p, d) = C(p, d)`。
- **去重累计**：本地代价被四个方向各计一次，聚合时减去 3 份：
  `S(p, d) = Σ_r L_r(p, d) - 3 · C(p, d)`。
- **并列规则**：视差从 0 向上扫描、严格更小才更新，并列时取最小视差。

## 接口

```cpp
sgm::Aggregator agg(width, height, num_disparities, sgm::Params{p1, p2});
std::vector<int> disp = agg.compute(cost);   // cost: [(y*W+x)*D+d]
agg.path_cost(dir);          // 某方向 DP 后的路径代价 L_r
agg.direction_disparity(dir);// 某方向单独 WTA 的视差图（逐方向对照）
agg.aggregated_cost();       // 聚合代价 S
```

## 构建与运行

```sh
cmake -B build && cmake --build build
./build/sgm_test   # 单元测试：边界重启 / 归一化 / 去重 / P2 分支 / 并列规则
./build/sgm_demo   # 12x8 小图逐方向对照 + 单像素逐方向代价表
```
