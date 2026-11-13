# 加载器压缩导出表的共享节点与循环

插件加载器读取压缩导出表时，同一节点可被多个前缀共享，简单的全局visited会漏查名字，直接递归又会被恶意环拖死。给Go加载宿主实现可导入的导出解析包，按本题受限格式：根偏移0，每节点先读规范ULEB128的terminalSize，再取恰好该长度的终端区，随后一个字节childCount，每子边为非空ASCII的NUL终止标签及规范ULEB128绝对节点偏移。终端区为空表示非终端；否则flags只能为0或8，0后接地址ULEB128，8后接模块序号ULEB128与NUL终止转发名，空转发名使用当前完整导出名，终端区不得有剩余字节。节点在同一解析中按字节区间固定，重复到达必须得到完全相同边界；节点在同一解析中按字节区间固定，重复到达必须得到完全相同边界；允许不同路径共享完整节点，禁止路径环、节点字节区间部分重叠、重复完整名字及越界；ULEB最多64位。转发由宿主给模块表解析，跨模块转发也检测环；地址须落在对应模块允许执行的半开区间。先完成全部解析与绑定才发布导出集合，设置展开步数、名字字节数及导出数预算，超限返回未完成。用菱形共享、节点内偏移跳转、跨模块环和同名异目标验证不会少查或部分发布。

## 包 API

```go
import netsec "example.org/netsec/q0048"

modules := netsec.ModuleTable{
    1: {
        Table:      module1Table,
        Executable: []netsec.Range{{Start: 0x1000, End: 0x2000}},
    },
    2: {
        Table:      module2Table,
        Executable: []netsec.Range{{Start: 0x8000, End: 0x9000}},
    },
}

exports, err := netsec.Load(1, modules)
if errors.Is(err, netsec.ErrIncomplete) {
    // A budget was exhausted. exports is nil: nothing was partially published.
}
target := exports["name"] // field Module and Address
```

- `Module.Table` is the byte-zero-rooted compressed table for that module.
- `Module.Executable` contains half-open address intervals. A direct address
  is accepted when `Start <= address && address < End`.
- `Load` resolves forwards through `Module.Table` recursively and rejects
  cycles both within a module and across modules.
- `ParseExportTable` exposes the validated node DAG and is useful when the host
  wants to inspect or enumerate a table before binding it.

Budgets are optional. A zero `Limits` field uses the corresponding default:

```go
limits := netsec.Limits{
    MaxSteps:     10_000, // every path expansion counts, including shared nodes
    MaxNameBytes: 1 << 20,
    MaxExports:   4_096,
}
exports, err := netsec.LoadTableWithRanges(1, table, ranges, modules, limits)
```

`MaxSteps` counts node expansion per path, not per unique node. This is
deliberate: a diamond must expand the shared node again for each prefix so
that no export name is missed. `MaxNameBytes` is cumulative over generated
full names, and `MaxExports` counts distinct terminal names across all modules
reached by the load.

All structural, cycle, duplicate-name, forward-target, and address failures
return a nil export set. `errors.Is` works with `ErrInvalidTable`,
`ErrIncomplete`, `ErrForwardCycle`, `ErrDuplicateExport`,
`ErrAddressOutOfRange`, `ErrExportNotFound`, and `ErrModuleNotFound`.
