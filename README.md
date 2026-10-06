# rescheck

资源状态机交错检查器。输入若干资源状态机和多个有限动作脚本，枚举所有
保持各脚本内部顺序的交错（interleaving），寻找**全部脚本终止后仍有资源
未释放**的最短轨迹。动作失败同样产生公开（可观测）的状态转移，并记录
在轨迹中。找到的反例会用一个独立的轨迹回放器重新验证；探索预算耗尽
时返回 `unknown`。

## 用法

```sh
go run ./cmd/rescheck examples/leak.json
```

退出码：`0` = safe，`1` = violated（输出经验证的反例），`2` = unknown
（预算耗尽），`3` = 用法或规格错误。

## 输入格式

```json
{
  "resources": [
    {
      "name": "lock",
      "states": ["free", "held", "broken"],
      "initial": "free",
      "final": ["free"],
      "transitions": [
        {"from": "free", "op": "acquire", "to": "held"},
        {"from": "held", "op": "acquire", "to": "held", "result": "fail"}
      ]
    }
  ],
  "scripts": [
    {"name": "worker_a", "actions": [
      {"resource": "lock", "op": "acquire"},
      {"resource": "lock", "op": "release"}
    ]}
  ],
  "budget": 100000
}
```

- `final`：视为“已释放”的终止状态集合；脚本全部结束时，任一资源不在
  其 `final` 集合中即为违规。
- 转移的 `result` 为 `ok`（默认）或 `fail`；失败转移是公开边，会出现在
  轨迹里。未声明的 `(state, op)` 组合按**公开失败自环**处理（动作可观测地
  失败并留在原状态），而非静默阻塞。
- 每个 `(from, op)` 至多一条转移（确定性机器），否则校验报错。
- `budget`：最多探索的全局状态数，缺省 100000；耗尽返回 `unknown`。

## 判定语义

- 全局状态 = 各资源机状态 × 各脚本程序计数器；BFS 枚举所有保序交错，
  因此返回的反例是**最短**违规轨迹。
- 反例在报告前由 `Spec.Verify` 独立回放：逐步校验脚本顺序、每步的
  `from`/`to`/`result` 与状态机一致、所有脚本跑完、且终态确有资源未
  释放。回放器与探索器不共享状态，探索器的 bug 无法自证其反例。

## 输出

```json
{
  "status": "violated",
  "trace": [{"script": "...", "resource": "...", "op": "...",
             "result": "ok|fail", "from": "...", "to": "..."}],
  "leaked": ["lock@broken"],
  "explored": 12,
  "reason": "terminated with unreleased resources: lock@broken"
}
```

## 测试

```sh
go test ./...
```
