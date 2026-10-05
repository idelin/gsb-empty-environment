# shadow — 规则遮蔽分析器

按"顺序首次匹配"语义分析权限规则，找出永远无法生效的规则，并给出覆盖它的最小前序规则集合；对部分遮蔽的规则给出可生效的具体见证请求。

## 语义模型

- 请求 = (主体, 动作, 路径)。主体域、动作域为有限集合；路径由有限词汇表的片段组成，深度不超过 `maxDepth`，因此请求空间有限、可枚举。
- 规则按顺序求值，第一条匹配 (主体, 动作, 路径) 的规则生效，其余不生效。
- 路径模式：以 `/` 分隔的片段序列；`*` 匹配任意单个片段；`**` 只能出现在末尾，匹配任意数量（含零个）的剩余片段；其余为字面量。不带 `**` 的模式要求路径长度与模式长度相等。

## 分析判定

对每条规则 `r`，设其匹配请求集为 `M(r)`，前序规则匹配集的并集为 `U`：

- `M(r)` 为空 → `empty`（匹配不到任何请求）。
- `M(r) ⊆ U` → `fully-shadowed`，永远不会生效。输出覆盖它的**最小前序规则集合**（最小基数的精确集合覆盖，按语义匹配集计算，而非文本比较；可能需要多条前序规则的并集）。
- 否则规则可生效，返回见证 `M(r) \ U` 中的具体请求：
  - 与 `U` 无交集 → `effective`；
  - 有交集 → `partially-shadowed`。

## 输入格式

```json
{
  "subjects": ["alice", "bob"],
  "actions": ["read", "write"],
  "segments": ["home", "etc", "tmp"],
  "maxDepth": 3,
  "rules": [
    {"effect": "deny",  "subjects": ["*"],     "actions": ["write"], "path": "etc/**"},
    {"effect": "allow", "subjects": ["alice"], "actions": ["*"],     "path": "etc/*"}
  ]
}
```

- `subjects` / `actions` 中 `"*"` 表示整个域；也可以列出名子集（必须在域内）。
- `effect` 为 `allow` 或 `deny`（缺省 `allow`），仅作展示，不影响遮蔽判定。
- 请求空间大小（|subjects| × |actions| × 路径数）超过 5,000,000 会报错，需缩小域或 `maxDepth`。

## 使用

```sh
go build -o shadow .
./shadow examples/rules.json   # 文本报告
./shadow -json rules.json      # JSON 报告
./shadow -check rules.json     # 存在 fully-shadowed 规则时退出码为 1
```

## 输出示例

```
rule 8: deny subjects={*} actions={*} path="var/**"
  status: fully-shadowed (never takes effect)
  covered by: rules [5 6 7]
```

表示规则 8 被规则 5、6、7 的并集完全覆盖（任何单条前序规则都无法覆盖它）。

## 测试

```sh
go test ./...
```
