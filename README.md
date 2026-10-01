# spaneval — 声明式 span 结构评估器

根据**声明的预期 span 结构**评估**实际 span** 的缺失、重复与未结束比例。
纯本地合成遥测事件、由 JSON 夹具回放；不依赖任何监控平台，也不是展示看板。
仅使用 Go 标准库。

## 运行

```sh
go run ./cmd/spaneval run fixtures/checkout_ok.json   # 回放单个夹具
go run ./cmd/spaneval run fixtures/*.json             # 回放全部夹具
go run ./cmd/spaneval run -json fixtures/*.json       # JSON 输出
go run ./cmd/spaneval formula                         # 打印公开分数公式
go test ./...                                         # 全部测试
```

## 预期结构：有限规则图

`expected` 是一棵有限规则树（`internal/eval/model.go`）：

- `span`：具体 span 规则，含 `name`、重复次数 `[min, max]`、条件 `when`
  （按 trace 属性剪枝，不满足条件的节点不参与评分）。
- `all`：所有子节点都被期望。
- `choice`：互斥分支，恰好期望一个分支出现；两个分支都出现即违规。

## 匹配：最低代价映射（非贪心计数）

匹配器（`internal/eval/match.go`）是小树的**全映射枚举参考实现**：

- 重复次数展开为槽位（前 `min` 个为必需），`choice` 分叉出候选槽位表。
- 枚举实际 span 到槽位的所有映射，取罚分最低者；每个 span 至多映射一次。
- **父子与稳定身份约束**：子槽位只能在已映射父 span 的实际子 span
  （按 span ID，而非名称）中匹配；必需槽位只有在不存在兼容实际 span 时
  才可判缺失——trace 不能靠"无视自己发出的 span"换取更低代价。
- 实际 span 分类：`missing`（槽位无 span）、`duplicate`（超出 max）、
  `unclosed`（已匹配但无 end 事件，**与确实缺失分开**）、
  `misplaced`（名称在图中但父级错误）、`unexpected`（图中没有该名称）。
- **预算**：枚举状态数超过 `budget` 时判定 `INDETERMINATE`，
  不输出任何确定分数。

## 公开分数公式

```
R     = 必需槽位数（>= 1）
score = 100 * clamp( matched/R
                     - (0.5*duplicates + 0.25*unclosed
                        + 0.75*misplaced + 0.25*unexpected) / R, 0, 1 )
```

- `matched` 只统计唯一的必需槽位；**重复不提高完整度**，结构错误不能
  用数量抵消（`fixtures/duplicates.json`：3 个 worker 也补不上缺失的
  cleanup，得分 33.3）。
- **缺根即关键**：根 span 缺失时后代无法确立身份，全部记为 misplaced，
  trace 标记 CRITICAL 且 `score = min(score, 10)`
  （`fixtures/missing_root.json`：子 span 再多也是 0 分 + CRITICAL）。

## 迟到 span 与分数修订

夹具的 `evaluate_after` 把事件流切成两段：先评估前缀，随后每个迟到事件
触发一次重评估；判定变化时输出修订报告与解释（哪类计数从几变到几、
分数从多少变到多少）。见 `fixtures/late_arrival.json`：end 事件迟到只
消除 `unclosed`，start 事件迟到才消除 `missing`——两者始终分开。

## 目录

```
cmd/spaneval/main.go     CLI（run / formula）
internal/eval/model.go   规则图、事件、夹具加载与校验
internal/eval/match.go   全映射枚举匹配器（预算保护）
internal/eval/score.go   公开权重与分数公式
internal/eval/eval.go    评估、回放、迟到修订与解释
fixtures/*.json          可回放夹具
```
