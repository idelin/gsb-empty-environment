# 头里禁止嵌套JSON

受保护头用重复键，后出现的alg覆盖先出现的。头和载荷都拒绝重复键；alg以第一次出现为准的做法不允许，直接失败。键不重复的头继续验证。验证组件调用可导入的Go解码器做这次裁决。在头里写两次alg。

## 裁决规则

- 受保护头与载荷都通过 `strictjson` 解码器解析，对象在任意嵌套层级出现重复键即整体失败，错误可由 `errors.Is(err, strictjson.ErrDuplicateKey)` 识别。
- 不采用“后者覆盖前者”，也不采用“`alg` 以第一次出现为准”；头中写两次 `alg` 直接拒绝，即使签名有效也不继续验证。
- 键不重复的头与载荷正常解码并继续 HMAC 签名校验（HS256/HS384/HS512）。
- 标准库 `encoding/json` 对重复键默认保留最后一个值，`strictjson` 以流式 token 扫描在解码前拦截该歧义。

## 包结构

- `strictjson`：可导入的严格 JSON 解码器，提供 `Unmarshal`、`NewDecoder`，重复键返回包装 `ErrDuplicateKey` 的 `*DuplicateKeyError`。
- 根包 `netsec`：`VerifyCompact(token, secret)` 验证紧凑序列化 JWS，内部对头和载荷分别调用 `strictjson.Unmarshal`；另提供 `EncodeCompact` 供测试与签发使用。

## 复现“头里写两次 alg”

`jws_test.go` 中的 `TestVerifyRejectsHeaderWithDuplicateAlg` 构造受保护头

```json
{"alg":"HS256","typ":"JWT","alg":"none"}
```

并用合法 HS256 签名签发，`VerifyCompact` 仍返回包装 `netsec.ErrDuplicateKey` 的错误。

## 运行

```sh
go vet ./...
go test -race ./...
```
