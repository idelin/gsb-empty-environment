# recordio

自描述记录流 + 防错配随机访问索引，仅用 Go 标准库。

## 字节格式

```
file   = "RCD1" record*
record = bodyLen u32 LE | schemaVersion u16 | fieldCount u16 | field*
field  = fieldID u16 LE | fieldLen u32 LE | payload(fieldLen)
```

- `bodyLen` 是记录长度（长度前缀之后的字节数），最小值 4。
- 记录携带 `schemaVersion`；字段以 ID 标记。读方用 `Record.FieldByID` 只取
  认识的 ID，未知字段按 `fieldLen` 整段跳过，实现跨版本定位。
- 索引文件 `IDX1`：固定头 + 源文件 SHA-256 摘要 + 记录数 + 每记录 u64 偏移。

## 语义约定

- **任意分块**：`Parser.Feed` 接受任意大小块（单字节亦可）；帧未到齐时
  `Next` 返回 `ErrNeedMoreData`（缺数据），格式被证伪时返回 `ErrBadData`
  （坏数据），二者严格区分。
- **坏数据不重同步**：一旦出现坏长度/坏字段，Parser 进入永久失败态，再喂
  合法数据也不会错位解析成功。
- **先校验再分配**：记录声明长度先过最小长度与配额（默认 1 MiB）检查；
  字段 payload 仅在确认不超出 body 后才分配。
- **真实边界**：`parseBody` 要求字段恰好耗尽整个 body，有任何残余字节即
  坏数据；`ReadRecordAt` 在索引偏移处独立重解析，命中点不可能落在别的
  记录中间。
- **空记录**：零字段是合法帧；**空文件**指仅含魔数的文件，合法且索引为空；
  0 字节文件（无魔数）与截断流一律报 `ErrBadData`。
- **摘要防错配**：`Index.VerifySource` 校验源文件 SHA-256，篡改或换文件即
  拒绝使用旧索引。

## 运行

```sh
go test ./... -v     # 15 个测试
go run ./cmd/demo    # 端到端样例：建流/建索引/随机访问/篡改检测
```
