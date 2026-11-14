# 接口签名规范化组件

`example.org/netsec/q0488` 为 Go 签名服务提供唯一的规范化字节序列。签名和验签都必须覆盖 `Canonicalize` 返回的字节，不能直接拼接原始参数。

## 固定规则

- 字段按未转义 key 的 UTF-8 字节升序排列。
- 只保留 RFC 3986 unreserved 字符（`A-Z`、`a-z`、`0-9`、`-`、`.`、`_`、`~`），其余字节写成大写 `%HH`。
- 空格固定为 `%20`，拒绝有歧义的 `+`；`+` 作为数据时编码为 `%2B`。
- 字面 `%` 编码为 `%25`，因此 `%20` 与空格、双重编码 `%2520` 与 `%20` 都是不同内容。
- 空值写成 `key=`；null 写成裸 `key`，两者不互相推断。
- 只做一次百分号解码；不做 Unicode 规范化，NFC/NFD 是不同内容。
- 重复 key、空 key、非法 UTF-8、控制/格式字符、BOM、Unicode 行分隔符与 noncharacter、畸形转义、裸 `+`/`=`/`#` 均拒绝。

## 使用

```go
canonical, err := netsec.Canonicalize(netsec.Fields{
    {Key: "b", Value: "2"},
    {Key: "a", Value: "hello world"},
})
// canonical == []byte("a=hello%20world&b=2")

signer := netsec.NewHMACSigner(secret)
signature, err := signer.Sign(canonical) // 签名字节就是上面返回的值
ok, err := signer.VerifyCanonicalQuery("a=hello%20world&b=2", signature)
```

`Sign`/`Verify` 对 `string`、`[]byte` 输入要求已经是规范形式；非规范输入返回 `ErrNonCanonical`。`VerifyCanonicalQuery` 同样拒绝任何重编码，`VerifyQuery` 则先做一次规范化，适合需要兼容等价拼写的边界层。

`canonical` 子包导出同一套 API，便于签名服务按包名导入：

```go
import "example.org/netsec/q0488/canonical"
```

运行安全用例：

```sh
go test ./...
```
