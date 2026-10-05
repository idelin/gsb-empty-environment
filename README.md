# intervalround

精确有理多区间运算 + 保守十进制包围（Go）。

- `Bound`/`Interval`/`Multi`：端点可有限（`big.Rat` 精确值）或 ±∞，均带开闭标记；单点 `[x,x]` 闭合非空，空集为零段。
- `Of`/`Closed`/`Point`/`All`/`Empty` 构造；`Contains(x *big.Rat)` 用有理点验证包含。
- `Intersect`、`Union`、`Div` 精确运算；除法在零处拆分，跨零产生多段（如无界两段）。
- 段数超过 `maxSegs` 时只按最小间隙保守合并（凸包），结果恒为超集。
- `Round(digits)`：左端向下、右端向上舍入到 `10^-digits` 网格；恰好落在网格上的端点保留原开闭标记，被外移的端点取闭，保证包围关系不被破坏。

运行测试：`go test ./...`
