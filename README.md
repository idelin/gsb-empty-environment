# 概率程序输出合并

安全研究平台需要精确比较两个随机认证模型的失败概率。写供Go验证宿主调用的有限概率解释器，语句含有理概率分支、布尔赋值、有限循环和观察输出；相同状态合并质量但保留来源，拒绝概率和不为一的节点。返回每个可观察结果的精确分布，用逐路径枚举核对嵌套循环及提前异常。

## 用法

包 `netsec` 只依赖标准库，概率用 `math/big.Rat` 精确表示，无浮点误差。

```go
p := &netsec.Program{Name: "auth", Body: []netsec.Stmt{
    netsec.Flip{NodeID: "auth", Branches: []netsec.Branch{
        {Weight: big.NewRat(99, 100), Body: []netsec.Stmt{
            netsec.Observe{Label: "result", Expr: netsec.Lit(true)},
        }},
        {Weight: big.NewRat(1, 100), Body: []netsec.Stmt{
            netsec.Fail{Name: "auth_fail"},
        }},
    }},
}}

dist, err := p.Distribution()   // 合并解释器：精确结果分布
paths, err := p.Enumerate()     // 独立预言机：逐路径枚举
report, err := p.Verify()       // 两引擎交叉核对，含来源校验
_ = dist.FailureProb()          // 所有异常结果的总失败概率
```

## 语义

- `Flip`：有理权重分支；节点缺标识、负权重、无分支或权重和不等于 1 时返回 `NodeError`，零权重分支跳过。
- `Assign` / `Observe`：布尔变量赋值；`Observe` 向输出轨迹追加 `(标签, 布尔值)`，读取未定义变量报错。
- `Repeat` / `While`：有限循环；`While` 超过声明上限返回 `LimitError`，帧按嵌套层级独立计数。
- `Fail`：抛出命名异常并立即终止，保留异常前的全部观察。
- 合并：相同 `(程序计数, 变量状态, 循环帧, 节点访问计数, 观察轨迹)` 配置合并为一个节点，质量求和；每个来源路径及其精确质量单独保留，循环中重复访问同一节点用出现序号区分。

## 验证

`Verify` 同时运行合并解释器与 CPS 风格的逐路径枚举器，断言：枚举路径质量和为 1、每个可观察结果两侧一致、每条保留来源都能在枚举结果中找到且质量匹配。测试覆盖嵌套循环、循环内概率分支的提前异常、状态合并与来源保留、非法节点拒绝及两个认证模型的失败概率对比。

```sh
go test ./...
```
