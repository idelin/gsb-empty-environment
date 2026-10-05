# beatfit — 节拍网格拟合器

给定一条含**漏拍**和**多余敲击**的敲击时间序列，从有限的整数周期与整数起点候选中，
找出总代价最小的节拍网格 `{ start + k*period : k ∈ Z }`。

## 代价模型

敲击与网格点之间按**保序对齐**（类似序列比对，匹配保持先后顺序，一一对应）：

- 匹配对 `(t, g)`：代价为时间偏差 `|t - g|`；
- 漏拍：位于首个与末个匹配网格点之间、未被匹配的网格点，每个罚 `missPenalty`；
- 多余点：未被匹配的敲击，每个罚 `extraPenalty`；
- 匹配跨度之外的网格点不计代价。

总代价 = 匹配偏差 + 漏拍罚分 + 多余点罚分。对每个候选 `(period, start)`
用动态规划求最优对齐（`O(n·m)`，`n` 为敲击数，`m` 为窗口内网格点数），
再枚举全部候选取全局最小——**不是**对相邻时间差取众数（众数在漏拍多时会给错周期，
见 `TestNotJustModeOfDiffs`）。

## 输入（JSON，stdin 或 `-input` 文件）

```json
{
  "taps": [0, 98, 201, 302, 410, 450, 505, 700, 803, 905],
  "periods": [100, 101],
  "periodMin": 90, "periodMax": 110,
  "starts": [0, 1],
  "startMin": 0, "startMax": 99,
  "missPenalty": 40,
  "extraPenalty": 35
}
```

- `periods` 与 `periodMin/periodMax` 可单独或合并使用（取并集，周期须为正整数）；
- `starts` 与 `startMin/startMax` 同理；**完全省略起点时，对每个周期搜索全部
  `start ∈ [0, period)`**（网格只取决于 `start mod period`）；
- `missPenalty`、`extraPenalty` 须为非负整数。

## 输出

文本模式（默认）打印：最优代价、最优网格、对应关系（匹配对及偏差、漏拍网格点、
多余敲击）、以及**全部并列周期**（达到最小代价的每个周期及其全部最优起点）。
加 `-json` 输出机器可读的同样内容。

## 使用

```sh
go build -o beatfit .
./beatfit -input example.json          # 文本输出
./beatfit -json < example.json         # JSON 输出
go test ./...                          # 运行测试
```
