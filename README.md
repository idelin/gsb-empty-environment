# fscrypt策略继承与跨目录移动

攻击者把明文文件或另一密钥加密的文件移动、硬链接进某个加密目录，使目录内文件不受同一策略保护。为Go文件系统组件写可导入的 fscrypt 策略继承模型：新建项继承父目录策略（版本、主密钥标识、内容与文件名加密模式、flags）；rename 与 link 要求源与目标目录策略完全一致，明文文件不得进入加密目录，加密文件不得进入明文目录；非空目录不能设策略，已有策略不可更改。测跨目录移动、硬链接、重命名交换。

## API

The module root is the importable `netsec` package:

```go
import netsec "example.org/netsec/q0311"

policy := netsec.Policy{
    Version:             netsec.PolicyVersionV2,
    MasterKeyIdentifier: "master-a",
    ContentsMode:        netsec.ContentsModeAES256XTS,
    FilenamesMode:       netsec.FilenamesModeAES256CTS,
    Flags:               netsec.FlagDirectKey,
}

fs := netsec.NewFS()
_ = fs.Mkdir("/secret")
_ = fs.SetPolicy("/secret", policy) // only allowed while empty
_ = fs.CreateFile("/secret/file")   // inherits the complete policy

err := fs.Rename("/plain/file", "/secret/file")
// errors.Is(err, netsec.ErrPlaintextIntoEncrypted)
```

## Enforcement Model

- `Policy` is comparable; equality covers version, master key identifier, contents mode, filenames mode, and flags.
- `Mkdir` and `CreateFile` copy the parent directory policy exactly.
- `SetPolicy` succeeds only on an empty directory with no policy. A set policy cannot be replaced or removed.
- `Rename` and `Link` require the source and destination parent directories to have exactly equal policies. The item's own policy must also match the destination.
- A plaintext item cannot enter an encrypted directory, and an encrypted item cannot enter a plaintext directory.
- `RenameExchange` checks both directions before changing either directory entry.
- Policy checks and tree mutation happen under one lock, so they cannot race.

An encrypted directory created below a plaintext parent is a policy root. It may be moved between plaintext parents, but never below an encrypted parent or into a namespace whose policy differs. Hard links to directories are rejected.

Run the tests with:

```sh
go test ./...
```
