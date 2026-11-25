# closure — 制品闭包签发验收

`closure` 是供 Go 签名宿主调用的零第三方依赖闭包验收包。它在同一个共享 ROBDD
管理器中生成并约简 `pass`、`reject`、`unbuildable` 三个区域，并为拒绝区域提供可
由原始输入独立复核的选择与根引用链证据。

模块路径：`prescreen.local/ps-c690d01eea`，根包名：`closure`。

## 判定语义

- 身份 `Identity` 精确包含 `Name`、整数 `Version`、`Source`、`Digest`；来源或摘要
  不同即不同身份，证明不能共用。
- `Request.VariableOrder` 给出最多 24 个共享布尔标志的 BDD 变量序。
- `Request.Clauses` 是二元布尔子句的合取，定义“允许配置”。
- 每个 `Dependency` 可给出多个精确备选身份；`When`/`Condition`/`Flag` 是标志值
  合取条件，空条件表示恒真。
- 同一个包名在一次解析中只能绑定一个精确身份；环通过已访问集合处理，只有根
  实际可达的身份进入闭包和证明检查。
- `Attestation` 绑定精确 `Identity`、生效 `Condition` 和
  `PolicyGeneration`。

对每个满足全局约束的配置：

- 没有任何合法解析：`unbuildable`。
- 存在至少一个含未证明身份的合法解析：`reject`。
- 所有合法解析中的全部闭包身份均受证：`pass`。

全局约束排除的配置不属于上述三个区域。三个 BDD 两两不交，并集恰好是
`Allowed`。

给定的根目标摘要默认作为签名宿主的信任锚，不要求根自身另有来源证明；如果
验收策略要求根身份也受证，设置 `Request.RequireRootProof = true`。

## 共享 ROBDD

`Analyze` 不按每个标志配置生成临时树再压缩。它枚举有限的完整解析选择，把每条
解析的条件支持集直接构造为规范化 BDD，再通过共享唯一表和 Apply 记忆化求并/交/
补。`Graph.Mk` 是唯一节点构造入口，会立即执行 `low == high` 归约和
`(variable, low, high)` 结构共享。

节点预算由 `Request.NodeBudget`（别名 `Budget`/`MaxNodes`）控制：

- 正数：最多分配的非终结节点数。
- `0` 或负数：不限制。

预算不足不是普通输入错误；`Analyze` 返回 `Report.Status == StatusUndecided`、
`Undecided == true`，且 `Cause` 可被 `errors.Is(..., ErrBudgetExceeded)` 命中。
宿主必须把它视为“未定”，不能视为通过。

## 拒绝证据与检查器

`Report.Evidence` 中每个 `Evidence` 包含：

- 一个满足其 `Support` BDD 的具体 `Config`。
- 每个包名的全局身份绑定 `Selection`。
- 每个可达包的每条依赖的 active/inactive 决策和选中身份 `Choices`。
- 从根到未受证身份的 `IdentityChain` 与边细节 `Chain`。
- 未受证原因：`no_attestation`、`generation_mismatch` 或
  `condition_false`。

调用 `VerifyEvidence(request, evidence)` 时不会使用分析器内部状态或 BDD 节点，
只使用输入候选表、证明表、子句和证据中的具体选择重放完整闭包。

## 示例

```go
package main

import (
    "fmt"

    closure "prescreen.local/ps-c690d01eea"
)

func main() {
    root := closure.Identity{Name: "root", Version: 1, Source: "example.com/root", Digest: "sha256:root"}
    lib := closure.Identity{Name: "lib", Version: 3, Source: "example.com/lib", Digest: "sha256:lib"}

    req := closure.Request{
        Root:          root,
        VariableOrder: []string{"release"},
        Packages: []closure.Package{
            {ID: root, Deps: []closure.Dependency{{
                Name:         "lib",
                Alternatives: []closure.Identity{lib},
            }}},
            {ID: lib},
        },
        Attestations: []closure.Attestation{
            {ID: root, PolicyGeneration: 1},
            {ID: lib, PolicyGeneration: 1, Condition: closure.Condition{"release": true}},
        },
        PolicyGeneration: 1,
        NodeBudget:       128,
    }

    report, err := closure.Analyze(req)
    if err != nil {
        panic(err)
    }
    if !report.Decided() {
        panic(report.Cause)
    }

    release := closure.Assignment{"release": true}
    fmt.Println(report.Region(release)) // pass

    for _, evidence := range report.Evidence {
        if err := closure.VerifyEvidence(req, evidence); err != nil {
            panic(err)
        }
    }
}
```

也可以把 JSON 请求直接传给 `closure.Analyze(jsonBytes)`。

## 验证

仓库内测试包含：

- 6 个开关、全部 64 个配置与独立非 BDD 枚举器 `ClassifyConfiguration` 对照。
- 120 个确定性随机小图的全配置对照和全部证据复核。
- 互斥标志分支不会被错误合并为同名包冲突。
- `f=false` 的已证明/无依赖分支不会被折叠进 `f=true` 的未受证传递依赖。
- 同名不同源身份不能共用证明。
- 环、根摘要-only 输入、策略代次不匹配和零变量顺序。
- 24 个变量但只有两个影响结果时，共享图仅分配 10 个非终结节点；预算为 1
  时明确返回 `StatusUndecided`。

运行：

```sh
CGO_ENABLED=0 go test -count=1 ./...
```
