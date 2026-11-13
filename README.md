# mdoc设备认证会话转录与摘要核对

移动驾照的验证端收到的响应里，攻击者可以转发另一个验证端会话中拿到的响应（中继），或夹带发行方没签过的数据项。给Go验证端实现可导入的 mdoc 响应校验包，输入已解码的 IssuerSigned（MSO 的 valueDigests 与摘要算法、validityInfo、DocType，以及各命名空间下每个数据项的 digestID、random、elementIdentifier、elementValue）、DeviceSigned（deviceNameSpaces 与设备签名）、验证端自己的 SessionTranscript 字节和请求的数据项集合，签名与哈希由回调提供：每个返回的数据项都要在 MSO 里按 digestID 找到摘要，且与重算结果相等；返回项必须是请求项的子集；validityInfo 的 validFrom 与 validUntil 须包含当前时刻；设备签名的待签内容是数组 ["DeviceAuthentication", SessionTranscript, DocType, DeviceNameSpacesBytes] 的规范编码，其中的 SessionTranscript 必须是本端自己的；MSO 里的 DocType 须与响应中的一致；同一 digestID 重复出现、random 短于 16 字节时拒绝。用中继他人转录、夹带未签名数据项、超出请求集合、过期 MSO 和重复摘要标识验证。

## 包结构

- `types.go`：已解码的 `Response`/`IssuerSigned`/`DeviceSigned`/`MobileSecurityObject` 输入类型、回调接口与哨兵错误。
- `cbor.go`：无第三方依赖的确定性 CBOR（RFC 8949）编码器，用于重算 IssuerSignedItem 摘要和构造 DeviceAuthentication。
- `verify.go`：入口 `VerifyResponse(resp *Response, opts *Options) error` 及全部校验规则。

## 校验规则

`VerifyResponse` 按以下顺序检查，任一失败即返回可由 `errors.Is` 匹配的哨兵错误：

1. `NewHash` 与 `VerifyDeviceSignature` 回调存在，且本端 `SessionTranscript` 非空（`ErrMissingCallback`、`ErrSessionTranscript`）。
2. 响应 `DocType` 与 MSO `DocType` 完全一致（`ErrDocTypeMismatch`）。
3. `validFrom <= now < validUntil`，`now` 由调用方通过 `Options.Now` 注入，零值取 `time.Now()`（`ErrExpired`）。
4. 对每个命名空间下的每个返回项：
   - `random` 不少于 16 字节（`ErrShortRandom`）；
   - 同一命名空间内 `digestID` 不得重复（`ErrDuplicateDigestID`，跨命名空间允许复用）；
   - `(namespace, elementIdentifier)` 必须属于请求集合（`ErrNotRequested`）；
   - MSO `valueDigests` 中必须存在该命名空间与 `digestID` 的摘要（`ErrDigestNotFound`）；
   - 对 tag 24 包裹的 IssuerSignedItem 规范编码重新哈希，与 MSO 摘要做恒定时间比较（`ErrDigestMismatch`）。
5. 用本端 SessionTranscript 构造
   `["DeviceAuthentication", SessionTranscript, DocType, DeviceNameSpacesBytes]`
   的规范编码，交给 `VerifyDeviceSignature` 回调验签（`ErrDeviceSignature`）。

`SessionTranscript` 按线上 tag 24 字节串原样嵌入；`DeviceNameSpacesBytes` 优先使用调用方给出的 `NameSpacesRaw` 原始字节，否则由解码后的 `NameSpaces` 规范重编码，二者皆空时编码为 CBOR null。

## 回调

```go
type Hasher func(algorithm string) (HasherSize, error)
type DeviceSignatureVerifier func(deviceKey any, deviceAuthCOSE, deviceAuthentication []byte) error
```

摘要算法字符串（如 `SHA-256`、`SHA-384`）原样透传给 `Hasher`；`deviceKey` 为 MSO 中的不透明 COSE_Key，COSE 结构的解析与密码学验签完全由回调负责，包本身只标准库依赖。

## 安全说明

- 中继响应的设备签名绑定的是受害者会话的 SessionTranscript，在本端转录下验签必然失败。
- 夹带项在 MSO 中没有对应 `digestID` 摘要（或重算摘要不一致），无法通过发行方摘要核对。
- 返回集合只能是请求集合的子集，防止验证端被诱导处理未请求的敏感项。
