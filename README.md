# configeval

Go 配置求值库：变量表达式可引用其他变量与**显式环境快照**，绝不读取运行时进程环境。

## 特性

- **表达式**：数字 / 字符串 / 布尔字面量，`+ - * /`（`+` 同时支持数值加法与字符串拼接），括号，字符串模板插值。
- **引用**：`${name}` 引用其他变量；`${env.KEY}` 引用环境快照。
- **错误引用链**：缺失引用、循环引用、类型错误均返回 `*RefError`，`Chain` 记录从求值入口到失败点的完整链路（如 `a -> b -> c`）。
- **依赖摘要**：成功的 `Result` 携带 `Deps`（全部传递依赖，排序）与 `Digest`（对自身表达式及所有传递依赖规范值的 SHA-256）。
- **冻结边界缓存**：`Freeze(env)` 越过冻结边界；缓存条目记录其传递环境足迹，仅当足迹与新快照完全一致时才复用——缓存永不越过冻结边界泄漏旧值，未受影响的子图则增量保留（见 `Stats`）。

## 示例

```go
cfg := configeval.NewConfig()
cfg.MustSet("port", "8080")
cfg.MustSet("url", `"http://${env.HOST}:${port}/api"`)

ev := configeval.NewEvaluator(cfg, configeval.Env{"HOST": "example.com"})
res, err := ev.Eval("url")
// res.Value  = "http://example.com:8080/api"
// res.Deps   = ["env.HOST", "port"]
// res.Digest = "…sha256…"

ev.Freeze(configeval.Env{"HOST": "internal"}) // 冻结边界：url 重算，port 缓存保留
```

## 测试

```sh
go test ./...
```
