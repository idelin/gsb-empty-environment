# wheel包RECORD与METADATA一致

包索引在收录 wheel 前核对压缩包，攻击者放入重名条目、不在 RECORD 中的文件，或让 METADATA 的名称版本与文件名对不上。为Go收录服务实现可导入的 wheel 校验包：压缩包条目名唯一且不含 .. 或绝对路径；.dist-info 目录名与 METADATA 的 Name、Version 按名称规范化（小写，连续的 -、_、. 合并为单个 -）和版本字面一致；除 RECORD 及其签名文件外，每个条目都必须在 RECORD 中，且 sha256= 后为 URL-safe base64 无填充、大小相符；文件名里的 tag 与 WHEEL 的 Tag 集合须一致。用重名条目、遗漏文件、规范化名称和哈希篡改验证。

## 用法

```go
import "example.org/netsec/q0451"

// 文件路径：同时校验 wheel 文件名中的名称、版本和 tag。
err := netsec.ValidateWheel("demo_pkg-1.0-py3-none-any.whl")

// 内存中的 wheel：可显式传入文件名以启用文件名相关校验。
err = netsec.ValidateWheelBytes(data, "demo_pkg-1.0-py3-none-any.whl")
```

`ValidateWheel` 也接受 `io.Reader`、`*zip.Reader` 和 `*zip.ReadCloser`。名称规范化函数 `NormalizeName` 单独导出，便于收录侧复用。
