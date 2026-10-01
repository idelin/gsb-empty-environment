# streamdec

增量、事件式的流解码器（Go，仅标准库）。

## 序列化范围（自定义，纯数据，不执行宿主代码）

```
value  := object | string | int | bool | null
object := '{' field* '}'
field  := string value          // string 为字段名
string := 'S' <十进制长度> ':' <长度个字节>
int    := 'I' ['-'] <十进制数字> ';'
bool   := 'T' | 'F'
null   := 'N'
```

例：`{"name":"Alice","age":30}` 编码为 `{S4:nameS5:AliceS3:ageI30;}`。

## 保证

- **任意字节暂停**：`Write` 可按任意大小的块喂入（包括逐字节），长度头拆多块也能正确解析。
- **事件式输出**：字段开始/结束（`FieldStart`/`FieldEnd`）与值（`String`/`Int`/`Bool`/`Null`/`ObjectStart`/`ObjectEnd`）。
- **单对象完整后才发布**：顶层对象未闭合前不返回任何事件，闭合后整批发布。
- **错误位置为绝对偏移**：`Error.Offset` 跨多次 `Write` 累计。
- **先验证长度再分配**：长度头超过 `MaxStringLen`（1 MiB）在读到 `:` 之前即拒绝；嵌套深度限制 `MaxDepth`。
- **`Finish` 遇未闭合对象/截断 token 报错**。
- **消费者取消**：`Reset` 释放当前对象的事件缓存与缓冲，解码器可复用。

## 运行

```sh
go test ./...
go run ./cmd/demo
```
