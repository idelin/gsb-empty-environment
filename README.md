# 验证码挑战竞争证明

认证服务将验证码绑定到请求，但并发验证和撤销可能让过期挑战成功。实现供Go测试宿主调用的历史检查包，挑战创建、替换、答复、消耗和授权分别记录线性化区间；只有一个当前挑战能授权一次，错误答复按固定计数限制。返回满足契约的线性顺序或反例，用短历史枚举核对挑战替换与旧答复竞争。

## 包结构

`netsec` 包（模块 `example.org/netsec/q0472`）为 Go 测试宿主提供完整的
线性化检查能力，无外部依赖。

- `spec.go`：五种操作（`Create`/`Replace`/`Answer`/`Consume`/`Grant`）的
  顺序参考规范与结果码（`OK`、`ErrNoChallenge`、`ErrExpired`、
  `ErrWrongAnswer`、`ErrConsumed`、`ErrLocked`、`ErrReplay`）。
- `checker.go`：基于 Wing–Wing–Gong 就绪集与状态记忆化的线性化搜索；
  失败时通过逐项删除生成极小反例，并附带死路前缀与阻塞操作。
- `verify.go`：独立校验宿主给出的操作排列是否满足区间约束并能驱动规范。
- `enumerate.go`：受限短历史枚举，覆盖替换与旧答复/授权竞争、双授权、
  错误计数锁定，并注入单结果变异反例。

## 核心契约

- 每个 `Operation` 记录 `[Invoke, Return]` 区间与观测 `Result`。
- 任一时刻只有一个当前挑战；`Grant` 仅对当前挑战成功且全局仅一次，
  成功后挑战进入 consumed 状态。
- `Replace` 原子退役当前挑战并安装新挑战；对旧挑战的替换返回
  `ErrExpired`，标识符重用返回 `ErrReplay`。
- `Answer` 不消耗挑战；错误答复达到固定上限（`DefaultWrongLimit`，
  可由 `History.WrongLimit` 覆盖）后挑战锁定，后续访问返回 `ErrLocked`。

## 用法

```go
report, err := netsec.Check(history)
if err != nil { /* 历史结构非法 */ }
if report.Linearizable {
    _ = netsec.VerifyOrder(history, report.Order) // 独立复核
} else {
    ce := report.Counterexample // 极小反例、死路前缀与原因
}

items, stats, err := netsec.EnumerateShortHistories(netsec.EnumerateConfig{
    MaxOps: 6, WrongLimit: 2, IncludeBugs: true,
})
```

## 验证

- `go test ./...` 运行规范单测、区间竞争用例、枚举交叉检查。
- 差分测试用全排列预言机（≤7 个操作）核对 `Check` 的判定与返回顺序。
- `go test -fuzz FuzzCheck` 持续随机化历史并与全排列预言机比对。
