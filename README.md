# 流式风控规则引擎（带时效窗口 + 规则热切换）

纯 Go 标准库实现，无第三方依赖。

## 能力

- **复合规则条件**：单笔金额上限 + 滑动窗口历史频次/累计金额
  （如 "10 分钟内转账超 3 次且累计金额超 1 万"）。
- **规则版本热切换**：`UpdateRules` 立即对新会话生效；
  活跃长事务会话钉住首次评估时的旧版本，直到 `EndSession` 后平稳过渡。
- **审计日志**：每次裁定记录实际使用的规则版本号、命中规则与原因。

## 结构

- `risk/engine.go` — 引擎核心（`Engine` / `Rule` / `RuleSet` / `Decision` / `AuditEntry`）
- `risk/engine_test.go` — 验收测试（`go test ./...`）
- `cmd/demo/main.go` — 运行样例（`go run cmd/demo/main.go`）

## 验收场景（见 TestHotSwitchAcceptance）

1. t=90 长事务 `S-long` 开始，按 v1 裁定；
2. t=100 规则库热切换为更严格的 v2；
3. t=101 长事务第 4 笔仍按 v1 放行（v2 下本应拦截）；
4. t=101 新会话 `S-new` 按 v2 裁定，第二笔被严格拦截；
5. 审计日志中两会话分别标记 `v1` / `v2`。

## 运行

```sh
go test ./... -v
go run cmd/demo/main.go
```
