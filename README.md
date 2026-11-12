# 隐私聚合边界缓存

匿名查询缓存必须绑定真实分组边界，否则扩大范围的请求可能复用已加噪结果形成差分泄漏。写供Go统计宿主调用的查询身份包，输入集合表达式与有理聚合参数，规范化等价的有限集合表达但保留邻接定义、裁剪规则和噪声代次；只有完全同机制才共享结果。用小全集枚举验证等价键与不同机制分离。

## 用法

```go
import "example.org/netsec/q0450/queryid"

domain, err := queryid.ParseSetExpr("(eu|us)&!test")   // 或 queryid.Union/Inter/Diff/Comp 构造
id, err := queryid.NewIdentity(domain, queryid.Mechanism{
    Agg:        "sum",
    Params:     []queryid.Rat{queryid.MustRat("1/2")},  // 有理参数，2/4、0.5 同键
    Adj:        queryid.AddRemove,                      // 邻接定义
    Clip:       queryid.ClipRule{Kind: "l1-contrib", Bound: queryid.MustRat("4")},
    NoiseEpoch: 7,                                      // 噪声代次
})
cacheKey := id.CacheKey() // 只有域语义与机制完全相同才得到同一个键
```

- `queryid.SetExpr.Canonical` 把语义等价的有限集合表达式规范化为同一键
  （交换/结合/分配/德摩根/吸收律及无影响原子消除），扩大范围必然换键。
- `queryid/…` 下测试用 8 元素小全集枚举两万余个表达式，
  验证等价键与语义掩码一一对应，并验证邻接、裁剪、噪声代次等机制字段全部参与身份。
