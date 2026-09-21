# endianconv

按声明 schema 在大端 / 小端记录之间转换。纯标准库（`encoding/binary`、`unsafe` 仅用于重叠检测）。

## 保证

- **浮点按字节反转**，不经过 float 值：NaN 载荷位、signaling NaN 不被规范化。
- **变长字段只转换长度头部**，载荷逐字节原样拷贝。
- **分配前检查长度额度**：头部声明超过 `Max` / `maxRecord` 立即报 `ErrRecordTooLarge`。
- **缺数据 ≠ 坏数据**：`ErrNeedMoreData`（继续喂）与 `ErrRecordTooLarge`（流已损坏）可 `errors.Is` 区分。
- **原地转换**：dst 与 src 为同一区域时安全；部分重叠 upfront 拒绝（`ErrOverlap`）；不相交任意。
- **双重转换恢复全部字节**（2000 组随机 schema/记录验证）。

## 用法

```go
s := endianconv.Schema{Fields: []endianconv.Field{
    endianconv.U16(), endianconv.F64(), endianconv.VarBytes(2, 1024),
}}
le, _ := s.ConvertRecord(beRecord, binary.BigEndian, binary.LittleEndian, 0)

dec := endianconv.NewDecoder(s, binary.LittleEndian, 4096)
dec.Feed(chunk) // 任意分块
rec, err := dec.Next() // ErrNeedMoreData -> 继续喂；ErrRecordTooLarge -> 坏数据
```

## 运行

```sh
go test ./...   # 测试
go run ./cmd/demo
```
