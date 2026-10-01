# stablewire

稳定字段编号的二进制序列化库（仅 Go 标准库）。

## 设计约束

- **字段用稳定编号**：tag = `fieldNum<<3 | wireType`，编号一经发布不改含义。
- **未知字段保留透传**：解码不认识的字段按原始 TLV 字节保存，重新编码时写回——旧程序转存新数据不丢字段。
- **新增可选字段兼容**：旧数据在新 schema 下正常解码；新数据在旧 schema 下解码时未知字段透传。
- **删除编号不能复用**：`Schema.Reserve(num, reason)` 永久保留，复用即报错。
- **解码给版本诊断**：帧头携带写入方 schema 版本，`Message.Diag` 报告版本差、未知字段编号与说明。
- **解码不执行宿主代码**：只构造 uint64/int64/bool/string/[]byte，无反射、无 gob、无插件。
- **先验证长度再分配**：长度前缀先与剩余输入和 `MaxFieldLen`(64MiB) 比较，通过后才分配。
- **字段号重复类型冲突拒绝**：同一编号在流中出现两种线类型即报错；schema 内重复定义同样拒绝。
- **缺必填字段报具体编号**：`MissingRequiredError.Nums` 列出全部缺失编号。

## 支持的序列化范围

uint64、int64(zigzag)、bool、string、[]byte。不支持浮点、嵌套消息、map、接口。

## 运行

```sh
go test ./...     # 测试
go run ./cmd/demo # 样例
```
