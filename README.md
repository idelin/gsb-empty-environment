# edt — 精确平方欧氏距离变换

对二值图的每个像素，求其到最近前景像素的**平方欧氏距离**及该来源像素的坐标。
多个来源等距时，取坐标 `(row, col)` 字典序最小者。

## 算法

Felzenszwalb–Huttenlocher 可分离抛物线包络法：先对每列、再对每行做一维变换，
时间复杂度 **O(H×W)**，与像素数成线性。

全程**不使用浮点**：

- 一维变换中，站点 `i` 的抛物线为 `P_i(k) = m·(k-i)² + f[i]·m + rank[i]`，
  其中 `rank` 是并列时的次序键，`m` 大于一切 `rank`。由于各站点 `rank` 互不相同，
  复合键 `(距离, rank)` 的字典序最小化等价于单一整数最小化，抛物线两两不会相等。
- 抛物线交点比较用整数向下除法完成：`P_q(k) < P_p(k) ⟺ k > num/den`，
  故 `q` 严格胜出的首个整数 `k` 为 `floor(num/den)+1`（`floorDiv`）。
- 第二趟把来源坐标的字典序编码为 `rank = srcRow·W + col`，使全局等距时
  精确选出字典序最小来源。

## 接口

```go
dist, src := edt.DistanceTransform(fg) // fg [][]bool，true 为前景
// dist[r][c]：到最近前景像素的平方距离
// src[r][c] ：该来源像素坐标
// 无前景时 dist 为 -1、src 为 {-1, -1}
```

int64 交点运算在约 10⁴×10⁴ 以内的图像上保证不溢出。

## 测试

- `TestExhaustive`：1×N、N×1、2×2…4×4 等形状的全部二值图与 O(n²) 穷举逐像素对照；
- `TestRandom`：400 张随机图（含大尺寸、各种密度）与穷举对照；
- `TestNoForeground` / `TestSingleRow` / `TestSingleColumn` / `TestTieBreakEquidistant`：
  无前景、单行/单列、水平/垂直/对角等距分界等边界情形。

```sh
go test ./...        # 正确性
go test -bench=.     # 线性扩展性基准
```
