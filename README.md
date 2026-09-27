# authz — 版本化时间段授权组件

Python 3.11 标准库实现，无第三方依赖。支持带有效区间的允许/拒绝规则、
拒绝优先、资源路径规范化、审计版本固定，以及两版本间最早权限差异分析。
`examples/` 与测试中的数据均为合成数据。

## 规则模型

策略文件为 JSON：

```json
{
  "version": "v1",
  "rules": [
    {
      "id": "r-deny-private",
      "effect": "deny",
      "subject": "*",
      "resource_prefix": "/root/private/*",
      "action": "read",
      "valid_from": null,
      "valid_to": null
    }
  ]
}
```

- `version`：策略（规则集）版本，审计结果固定记录该版本。
- `effect`：`allow` 或 `deny`；同时匹配时 **拒绝优先**，无任何规则匹配时隐式拒绝。
- `subject` / `action`：精确匹配，`*` 为通配。
- `resource_prefix`：绝对路径；尾部 `/*` 表示前缀匹配（含该目录本身），
  单独的 `*` 匹配全部，其余为精确匹配。
- `valid_from` / `valid_to`：ISO 8601 时间，`null` 表示无界；
  区间为左闭右开 `[valid_from, valid_to)`。

## 路径规范化

求值前先规范化资源路径：折叠重复分隔符、解析 `.` 与 `..`；
`..` 试图越过根（如 `/../etc`）时请求直接拒绝（`reason: invalid_resource`）。
因此 `/root/a/../private/x` 规范化为 `/root/private/x`，无法绕过拒绝规则。

## 命令行

### 授权判定

```sh
python3 authz.py authorize --policy examples/policy_v1.json \
    --subject alice --resource /root/private/secret --action read \
    --time 2026-01-15T00:00:00Z
```

- `--time` 省略时取当前 UTC 时间。
- 退出码：允许 `0`，拒绝 `1`。
- 标准输出为审计记录 JSON：

```json
{
  "policy_version": "v1",
  "decision": "deny",
  "reason": "explicit_deny",
  "request": {"subject": "...", "resource": "...",
              "normalized_resource": "...", "action": "...", "time": "..."},
  "matched_rules": [{"id": "r-deny-private", "version": "v1", "effect": "deny"}]
}
```

`reason` 取值：`explicit_allow` / `explicit_deny` / `implicit_deny` /
`invalid_resource: ...`。`matched_rules` 中每条规则都固定标注其所属版本。

### 版本差异分析

```sh
python3 authz.py diff --policy-a examples/policy_v1.json \
    --policy-b examples/policy_v2.json --resources examples/resources.json
```

- `--resources`：JSON 数组文件，给出有限资源集合。
- 主体/操作域取自两版本规则中出现的有限取值（含通配规则对应的合成探针）。
- 退出码：两版本等价 `0`，存在差异 `2`。
- 输出最早差异时间及见证请求：

```json
{
  "result": "different",
  "versions": ["v1", "v2"],
  "earliest_difference": {
    "time": "2026-03-01T00:00:00Z",
    "witness": {"subject": "alice", "resource": "/root/reports/q1", "action": "write"},
    "decisions": {"v1": "deny", "v2": "allow"}
  }
}
```

等价时输出 `{"result": "equivalent", "versions": [...]}`。
若差异在无界区域（所有规则边界之前/之后）即已成立，`time` 为该区域的
代表性探针时刻，并附带 `note` 字段说明。

正确性说明：任一请求的判定结果关于时间是分段常值函数，仅在在规则区间
边界处变化；因此枚举全部边界（及首尾之外各一个探针点）即可覆盖整条
时间轴，最早差异时间在该意义下是精确的。

## 测试

```sh
python3 -m unittest test_authz -v
```
