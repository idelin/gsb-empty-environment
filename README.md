# tcheck — 时态性质检查器

对带命题标签的有限状态机做显式状态枚举，检查两条性质：

- **安全性（safety）**：进入危险态之前必须已授权 —— 每条可达的、进入
  `danger` 状态的迁移，其源状态（或目标状态本身）必须带 `authorized` 标签。
  初始状态若直接是未授权的危险态也算违规。反例为到达违规迁移的最短路径。
- **活性（liveness）**：`request` 最终必然得到 `response` —— 在去掉所有
  `response` 状态的可达子图中做 Tarjan SCC 分解；若某个 `request` 状态
  能在该子图中到达非平凡 SCC（含自环），则存在一条无限执行使请求永远
  得不到响应。反例以「前缀 + 循环」（lasso）形式给出。

关键语义：

- 有限长的未响应路径（死锁）**不算**永久失败，只有真实的循环才算。
- 不可达状态不参与分析，不影响任何结论。

## 输入格式

```json
{
  "initial": "idle",
  "states": [{"name": "idle", "props": ["request"]}],
  "transitions": [["idle", "idle"]]
}
```

## 使用

```sh
go test ./...
go run ./cmd/tcheck examples/ok.json
go run ./cmd/tcheck -request req -response ack -danger crit -auth permit model.json
```

两个性质都成立时退出码为 0，任一失败为 1。命题名可用命令行参数覆盖。
