# kerb-replay：简化 Kerberos 委派演练复核工具

用 Go 重放一组简化的票据事件，在指定复核时刻回答：以某用户身份出发，
沿委派关系最终能抵达哪些服务，以及每条最短路径由哪些票据支撑。

## 建模约定

- 票据事件统一用 `kind=tgt|tgs` 表示，字段含主体、目标服务、`forwardable`
  位、生效窗口（`not_before` / `expires_at`）。
- 只评估 `query_time` 这一时刻：不在窗口内的票据进入 `ignored_tickets`，
  原因分别为 `expired`、`not_yet_valid`、`subject_mismatch` 或时间格式错误。
- BFS 的节点是“某服务持有的、以用户为主体的可转发证据票状态”。
  首次抵达某服务即为最短票据链；重复抵达记录为 `cycle_suppressed`，
  天然覆盖跨服务循环且保证终止。
- 每一跳委派生成一张合成票据 `synth:<from>-><to>#<hop>`，其有效期窗口
  继承证据票；因此证据票过期后，整条后续链路不可用。
- TGT（`kind=tgt`）只作为直达可达结果，不作为可发起委派的服务节点。

## 两套委派策略

| 策略 | 判定依据 | 对应现实机制 |
| --- | --- | --- |
| `constrained` | 源服务 `services[from].delegate_to` 名单 | 传统约束委派（KCD） |
| `resource` | 目标服务 `resource_allow[to]` 名单 | 基于资源的约束委派（RBCD） |

共同门控（任一不满足则该跳进入 `blocked`）：

1. 证据票在 `query_time` 处于有效期内（`evidence_expired`）；
2. 用户未设置禁止委派标志（`user_delegation_disallowed`）；
3. 证据票带 forwardable 位（`evidence_not_forwardable`）；
4. 目标服务在 `services` 中有定义（`unknown_target_service`）；
5. 对应策略的名单放行（`not_in_service_delegation_list` /
   `not_in_resource_allow_list`）。

源服务 `mode=unconstrained` 时忽略两侧名单，可二次委派到任意已定义服务，
但上述 1–3 的共同门控仍然生效。

## 用法

```bash
go run . -input examples/scenario.json                 # 同时输出两套策略
go run . -input examples/scenario.json -policy resource
go run . -policy constrained -input - < events.json    # 也支持标准输入
```

输出 JSON 结构：

- `ignored_tickets`：未参与重放的票据及原因；
- `policies.<策略>.reachable`：可达服务、跳数、身份链 `chain`、票据链 `tickets`；
- `policies.<策略>.blocked`：被门控/名单拒绝的边及原因；
- `policies.<策略>.cycles`：BFS 中被抑制的回边（跨服务循环证据）。

## 输入字段

```json
{
  "query_time": "RFC3339 时刻",
  "user": {"name": "主体", "delegation_disallowed": false},
  "tickets": [
    {"id": "T1", "subject": "主体", "service": "SPN", "kind": "tgs",
     "forwardable": true, "not_before": "...", "expires_at": "..."}
  ],
  "services": {
    "SPN": {"mode": "constrained|unconstrained", "delegate_to": ["SPN"]}
  },
  "resource_allow": {"目标 SPN": ["允许代表用户来访的中间服务 SPN"]}
}
```

## 测试

```bash
go test ./...
```

覆盖：跨服务循环抑制、最短票据链、过期证据票、用户禁止委派标志、
不可转发票据门控、两套策略差异、非约束服务二次委派、票据主体过滤、
非法输入校验。
