# pathcount

简单路径计数与分页工具（Go）。图最多 22 个顶点，统计 s 到 t
不重复顶点且经过指定必经点集合的路径条数，计数使用大整数。

## 设计

- `Version`：不可变的问题快照（n、边、s、t、必经点），`Token()`
  返回其哈希。计数与分页都绑定同一 `Version`，`Paginate` 校验
  token，保证二者对应同一版本。
- `Counter`：子集状态记忆化（状态 = 当前点 × 已访问位掩码），
  `math/big` 计数。到达 t 时必经点未齐计 0（提前到 t 是死路，
  t 不会被中途穿越）。`NewCounter(v, budget)` 的预算限制状态求值
  次数，耗尽时返回 `ErrBudgetExceeded`（未知），绝不返回伪 0。
- `Paginate(prefix, offset, limit, token)`：按顶点编号字典序（与
  Count 同一顺序）列举以 prefix 开头且覆盖必经点的路径，利用共享
  记忆化的子树计数整枝跳过，返回该前缀下的总数与一页路径。

## 用法

```go
v, _ := pathcount.NewVersion(4, [][2]int{{0,1},{1,3},{0,2},{2,3},{1,2}}, 0, 3, []int{2})
c := pathcount.NewCounter(v, -1) // 负预算 = 不限
n, _ := c.Count()                // 3
page, _ := c.Paginate([]int{0}, big.NewInt(1), 2, v.Token())
// page.Total = 3, page.Paths = [[0 2 1 3] [0 2 3]]
```

CLI（JSON 进/出）：

```sh
go run ./cmd/pathcount <<<'{"n":4,"edges":[[0,1],[1,3],[0,2],[2,3],[1,2]],"s":0,"t":3,"must":[2],"budget":-1,"op":"count"}'
```

## 测试

`go test ./...`：随机小图上与子集 DP 对照暴力全路径枚举，覆盖
提前到 t、孤点必经、必经点冲突（只能经 t 到达）、预算中断返回
未知、token 不匹配、非法前缀、K_n 闭式解大整数计数。
