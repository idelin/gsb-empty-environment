# typepipe — 异构数据类型转换与格式映射管道

纯 Go 标准库实现的跨数据库类型映射转换引擎，覆盖 MySQL、PostgreSQL 与
JSON 文档库之间的类型差异，坏字段落到安全默认值并输出数据清洗异常日志。

## 能力总览

- **TinyInt → Boolean**：`tinyint(1)` 的 0/1 映射为布尔；越域值告警并按非零即真处理
- **日期时间 → ISO 8601**：MySQL `datetime`/`timestamp`、PostgreSQL `timestamptz`、
  Unix 秒统一输出带 UTC 偏移的 RFC 3339 字符串（如 `2024-03-01T10:30:00+08:00`），
  朴素字面量按 `Options.TZOffset` 指定的源时区解释
- **enum → 整数字典码**：按 `enum('a','b',...)` 声明顺序从 1 编号（与 MySQL 内部索引一致），
  也支持显式 `EnumMap`；未登记值落到默认码 0 并记录 `unknown_enum`
- **UUID ↔ String**：校验并规范化为小写连字符形式，兼容无连字符 32 位十六进制；
  非法值落到零 UUID
- **溢出数值处理**：按目标类型区间（含 unsigned）钳制到边界并记录 `overflow` 日志
- **异常日志**：每条清洗异常包含字段名、原因、原始输入、处置动作与最终输出

## 预设映射规则（节选）

| MySQL | PostgreSQL | JSON 文档 | 目标类型 |
|---|---|---|---|
| `tinyint(1)` | `boolean` | `boolean` | `bool` |
| `tinyint/smallint/int/bigint` | `int2/int4/int8` | `integer` | `int`（带区间校验） |
| `datetime`/`timestamp` | `timestamp[tz]`/`date` | `date` | `timestamp`（ISO 字符串） |
| `enum(...)` | — | — | `int`（字典码） |
| `char(36)` | `uuid` | `uuid` | `uuid`（规范化字符串） |
| `varchar`/`text` | `text`/`varchar` | `string` | `string` |
| `decimal`/`double` | `numeric`/`float8` | `number` | `float` |
| `json` | `json`/`jsonb` | `object`/`array` | `json` |

## 用法

```go
p, _ := typepipe.NewPipelineFromSchema("mysql", []typepipe.Column{
    {Name: "created_at", Type: "datetime"},
    {Name: "status", Type: "enum('active','pending','banned')"},
    {Name: "score", Type: "smallint"},
}, typepipe.Options{TZOffset: "+08:00"})

out, logs := p.Process(map[string]any{
    "created_at": "2024-03-01 10:30:00",
    "status":     "ghost",   // 未登记枚举 -> 0 + unknown_enum 日志
    "score":      "99999",   // 溢出 -> 钳制 32767 + overflow 日志
})
// out["created_at"] == "2024-03-01T10:30:00+08:00"
```

## 运行样例

```sh
go run ./cmd/demo
```

输出（节选）：

```json
== 转换结果 ==
[{ "created_at": "2024-03-01T10:30:00+08:00", "status": 2, "score": 90, ... },
 { "created_at": "1970-01-01T00:00:00Z", "status": 0, "score": 32767, ... }]
== 数据清洗异常日志 ==
[{ "field": "score", "reason": "overflow", "input": "99999",
   "action": "clamped", "output": 32767 }, ...]
```

## 测试

```sh
go test ./...
```

结果：12 个测试全部通过（`ok typepipe`），覆盖 TinyInt→Boolean、
datetime→带 UTC 偏移 ISO 字符串、enum→字典码、UUID 互转、
溢出钳制、unsigned 区间、三种来源的预设映射及 MySQL 验收场景。
