# YAML别名展开预算

流水线定义使用 YAML 锚点、别名和 << 合并键，攻击者可嵌套少量别名展开出十亿级节点。为 Go 配置加载器写可导入的展开预算包，基于 yaml.v3 的 Node 树在实际展开前估算别名展开后的节点数、深度和合并键层数，任一项超限就拒绝，别名环也拒绝，同时保留合法合并语义和原始行号。用嵌套倍增别名、合并键数组、别名环和正常共享配置验证加载端未被调用。

## 用法

```go
import netsec "example.org/netsec/q0040"

root, stats, err := netsec.Preflight(data, netsec.DefaultLimits())
if err != nil {
    return err // ErrNodesExceeded / ErrDepthExceeded / ErrMergeDepthExceeded / ErrAliasCycle
}

// root 未被改写。继续使用原有加载路径即可保留 yaml.v3 的合并语义和行号。
var cfg Config
if err := yaml.Unmarshal(data, &cfg); err != nil {
    return err
}
```

需要直接断言“拒绝时加载端未被调用”时，使用 `DecodeWith`：

```go
err := netsec.DecodeWith(data, &cfg, func(b []byte, out any) error {
    return yaml.Unmarshal(b, out)
}, netsec.Limits{MaxNodes: 20000, MaxDepth: 100, MaxMergeDepth: 8})
```

`budget` 子包提供同一套 API，便于只导入预算组件：

```go
import "example.org/netsec/q0040/budget"
err := budget.Check(root, budget.DefaultLimits())
```

## 估算规则

- `Stats.Nodes` 是别名完全展开后的保守节点上限，包含文档节点、`<<` 键和合并值序列包装节点。
- `Stats.Depth` 是展开后的最大嵌套层数；别名节点被其目标值替换。
- `Stats.MergeDepth` 是沿 `<<` 边的最长合并链层数，合并值数组按同一层处理。
- 每个不同的 `*yaml.Node` 只计算一次，并在计数上做饱和加法；倍增别名不会导致估算过程本身指数爆炸或整数溢出。
- `Limits` 中非正数字段表示该维度不设限；`DefaultLimits` 提供适合流水线配置的默认上限。

仓库内 `vendor/gopkg.in/yaml.v3` 提供本任务所需的 Node、锚点/别名、合并键和行号兼容层，使离线构建也可运行测试。生产环境若已有官方 `gopkg.in/yaml.v3`，Node API 与上述用法一致。
