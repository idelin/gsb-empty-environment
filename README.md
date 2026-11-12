# minisign可信注释约束

更新包的非可信注释不受文件签名覆盖，攻击者可借改写版本、时间或文件名回滚更新。为Go自更新库实现minisign验证包，供下载完成回调使用：同时验证文件签名与覆盖签名及可信注释的全局签名，只依据可信注释中的file、timestamp、version决策，文件名须与目标一致，时间戳不得早于已接受版本。用改注释、换文件和旧包重放验证；非可信注释仅用于展示。

## 实现

纯标准库实现（无外部依赖），包含一个自包含的 BLAKE2b（RFC 7693），因为 minisign
默认的 `ED` 算法对内容做 BLAKE2b-512 预哈希。

- `ParsePublicKey` / `ParseSignature`：解析 minisign 公钥与签名文件。
- `Verify`：同时校验两道签名。
  - 文件签名：`Ed25519(Ed || BLAKE2b-512(content))`（`ED`），或对原始内容的
    旧版 `Ed` 算法。
  - 全局签名：`Ed25519(filesig || trusted_comment)`，它认证可信注释。
  - 非可信注释不参与任何签名运算，仅保留用于展示。
- `ParseTrustedComment`：只从可信注释解析 `file`、`timestamp`、`version`，
  三者缺一即拒绝；时间戳支持 Unix 秒或 RFC 3339。
- `Verifier.VerifyUpdate`：下载完成回调入口，按顺序执行
  签名校验 → 可信注释解析 → 文件名与下载目标精确比对 → 时间戳防回滚。
  仅当全部通过才推进防回滚水位（`LastAccepted`），调用方随后才能安装并持久化该水位。

## 决策依据

- 版本、时间、文件名一律取自被全局签名覆盖的可信注释，绝不读取非可信注释。
- 可信注释中的 `file` 必须与下载目标文件名完全相等，否则返回 `ErrFileNameMismatch`。
- 可信注释时间戳早于已接受版本即返回 `ErrRollback`，且失败不移动水位。

## 测试

- BLAKE2b 对照 RFC 7693 向量（空串、`abc` 的 512/256 位），并覆盖 128/129/255/256/257
  等多块边界的流式写入与 `Reset`。
- 篡改可信注释（版本/时间/文件名）触发 `ErrBadGlobalSignature`。
- 替换或截断文件内容触发 `ErrBadSignature`；他钥签名触发 `ErrKeyIDMismatch`。
- 旧包重放触发 `ErrRollback`，且等时间戳不视为回滚、更新包仍可接受。
- 篡改非可信注释不影响校验结果，决策仍只来自可信注释。

```sh
go test ./... -race
```
