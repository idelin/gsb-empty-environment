# IMA 签名属性评估

供执行（`execve`）与映射（`mmap`）前调用的 Go IMA 评估包。评估器把
`security.ima` 扩展属性视为不可信输入，并在解析前执行边界检查。

## 属性格式

签名属性头共 9 字节：

| 字段 | 宽度 | 说明 |
| --- | --- | --- |
| type | 1 | 签名类型为 `0x03` |
| version | 1 | 必须为 `2` |
| algo | 1 | IMA `hash_algo` 标识，未知值拒绝 |
| keyid | 4 | 大端 |
| siglen | 2 | 大端，必须与实际签名字节数完全相等 |

解析器要求 `len(attribute) == 9 + siglen`。长度不足、长度超出实际值或带有
尾随数据都会返回 `ErrLengthMismatch`，不会发生越界切片。

## 策略与缓存

- `Policy.RequireSignature` 标记必须携带签名的路径；这些路径上的摘要类型
  属性返回 `ErrSignatureRequired`。
- 所有签名属性的 `keyid` 必须在 `Policy.TrustedKeyIDs` 中，否则返回
  `ErrUntrustedKeyID`。
- 版本不符、未知算法、空签名分别返回 `ErrUnsupportedVersion`、
  `ErrUnknownAlgorithm`、`ErrEmptySignature`。
- 缓存按路径保存，但每次命中前都会重新 `stat` 并比较设备号、inode、大小、
  权限、mtime 和 ctime。通过任一硬链接写入都会改变共享 inode 的 ctime，
  因此原路径和硬链接路径的缓存都会立即失效。

## 用法

```go
evaluator := netsec.NewEvaluator(netsec.Policy{
    TrustedKeyIDs:    netsec.TrustedKeys(0x01020304),
    RequireSignature: netsec.PathMatcher("/usr/bin/", "/opt/protected/"),
})

if err := evaluator.BeforeExec("/usr/bin/tool"); err != nil {
    // 拒绝执行
}
if err := evaluator.BeforeMmap("/opt/protected/lib.so"); err != nil {
    // 拒绝映射
}
```

`Evaluator.Evaluate` 返回 `Result` 和错误；只有错误为 `nil` 且
`Result.Allowed` 为 `true` 时才应继续执行或映射。`Result.Cached` 可用于
观察本次是否命中缓存。

## 安全边界

本包负责把 `security.ima` 当作不可信字节做边界安全的结构解析、类型/
算法/长度校验、keyid 策略判断和缓存失效。它不执行公钥密码学签名验证：
在 IMA 部署中，签名载荷的密码学校验由内核/IMA 或调用方完成，本包只接受
已经由受信 keyid 对应的签名。调用方应紧邻 `execve`/`mmap` 调用
`BeforeExec`/`BeforeMmap`，避免在检查与使用之间留下过长的路径替换窗口。

测试使用真实的扩展属性覆盖以下攻击场景：

- 仅摘要属性用于要求签名的路径；
- 声明长度超出实际属性长度（包括 `uint16` 最大值）；
- 受信集合之外的 keyid；
- 文件写入后重执行及属性替换后的缓存失效；
- 硬链接另一路径的独立缓存与共享 inode 失效。

运行：

```sh
go test -race ./...
```
