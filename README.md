# typepipe — 异构数据类型转换与格式映射管道

跨数据库类型映射转换引擎（纯 Go 标准库实现），处理 MySQL、PostgreSQL 与
JSON 文档数据库之间的数据类型差异。坏字段一律落入**安全默认值**并产生
**数据清洗异常日志**（内存收集 + 日志输出双通道）。

## 预设映射规则

| 源库 | 源类型 | 目标规范类型 | 说明 |
|---|---|---|---|
| MySQL | `tinyint(1)` | `bool` | 0/1 → false/true；其他值 → 默认 false + 告警 |
| MySQL | `datetime` | `iso8601` | 按源时区（如 Asia/Shanghai）解析，输出带 UTC 偏移的标准 ISO 字符串，如 `2024-03-01T04:30:00+00:00` |
| MySQL | `enum(...)` | `dictcode` | 枚举标签 → 整数字典码；未知标签 → 默认 0 + 告警；未显式给字典时按列定义 1 起始自动派生 |
| MySQL | `smallint` | `int` | 越界数值按策略处理：`Clamp` 截断到边界 / `UseDefault` 替换为默认值，均记录告警 |
| MySQL | `char(36)` | `uuid` | 校验并归一化为小写连字符形式；非法 → nil UUID + 告警 |
| PostgreSQL | `uuid` | `string` | UUID → 字符串 |
| JSONDoc | `number` / `string` | `int` / `bool` / `iso8601` | 支持 `json.Number`、Unix 时间戳等形态 |

无匹配规则的字段原样透传，不产生告警。

## 目录结构

- `pipeline/pipeline.go` — 引擎、规则、字段类型、清洗问题（Issue）定义
- `pipeline/convert.go` — 各目标类型的转换函数与安全默认值
- `pipeline/pipeline_test.go` — `go test` 测试
- `cmd/demo/main.go` — 运行样例

## 运行样例

```console
$ go run ./cmd/demo
=== MySQL -> canonical ===
{
  "api_key": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11",
  "created_at": "2024-03-01T04:30:00+00:00",
  "id": 42,
  "is_active": false,
  "score": 127,
  "size": 0
}

=== PostgreSQL -> canonical ===
{
  "id": "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
}

=== JSONDoc -> canonical ===
{
  "port": 0
}

=== data cleaning exception log ===
[clean] field=is_active type=tinyint(1) value=7 reason="cannot interpret as boolean" effect=default false
[clean] field=size type=enum(small,medium,large) value=huge reason="value not in enum dictionary" effect=default 0
[clean] field=score type=smallint value=300 reason="value 300 out of range [-128,127]" effect=clamped to 127
[clean] field=port type=number value=70000 reason="value 70000 out of range [0,255]" effect=default 0
```

## 测试结果

```console
$ go test ./... -v
=== RUN   TestTinyIntToBool
--- PASS: TestTinyIntToBool (0.00s)
=== RUN   TestDateTimeToISO8601UTCOffset
--- PASS: TestDateTimeToISO8601UTCOffset (0.00s)
=== RUN   TestEnumToDictCode
--- PASS: TestEnumToDictCode (0.00s)
=== RUN   TestEnumDictDerivedFromColumnDefinition
--- PASS: TestEnumDictDerivedFromColumnDefinition (0.00s)
=== RUN   TestIntOverflowClampAndDefault
--- PASS: TestIntOverflowClampAndDefault (0.00s)
=== RUN   TestUUIDStringInterop
--- PASS: TestUUIDStringInterop (0.00s)
=== RUN   TestConvertRecordEndToEnd
--- PASS: TestConvertRecordEndToEnd (0.00s)
=== RUN   TestJSONDocSources
--- PASS: TestJSONDocSources (0.00s)
=== RUN   TestUnmappedTypePassesThrough
--- PASS: TestUnmappedTypePassesThrough (0.00s)
PASS
ok  	typepipe/pipeline
```
