# fscrypt策略继承与跨目录移动

攻击者把明文文件或另一密钥加密的文件移动、硬链接进某个加密目录，使目录内文件不受同一策略保护。为Go文件系统组件写可导入的 fscrypt 策略继承模型：新建项继承父目录策略（版本、主密钥标识、内容与文件名加密模式、flags）；rename 与 link 要求源与目标目录策略完全一致，明文文件不得进入加密目录，加密文件不得进入明文目录；非空目录不能设策略，已有策略不可更改。测跨目录移动、硬链接、重命名交换。

## 模型

`FileSystem` 是一个并发安全的内存文件系统模型。目录通过 `*Policy`
保存策略，普通文件与子目录在创建时复制父目录的完整策略。`nil` 策略表示
明文目录；零值 `Policy` 仍是一项有效策略。

策略相等要求以下字段全部相同：

- `Version`
- `MasterKeyID`（16 字节主密钥标识）
- `ContentsMode`
- `FilenamesMode`
- `Flags`

`Rename`、`Link` 和 `Exchange` 在任何修改发生前完成全部校验：

- 源父目录与目标父目录策略必须完全相同；
- 被移动或链接的 inode 也必须属于目标父目录策略；
- 明文项不能进入加密目录，加密项不能进入明文目录；
- 交换操作同时校验两个方向，失败时命名空间保持不变。

`SetPolicy` 只接受尚无策略的空目录。策略一旦设置便不可修改，`Exchange`
等操作也不能通过目录子树制造循环。

## 使用

```go
package main

import (
	"fmt"

	netsec "example.org/netsec/q0311"
)

func main() {
	fs := netsec.NewFS()
	policy := netsec.Policy{
		Version:       netsec.PolicyVersion2,
		MasterKeyID:   [16]byte{1, 2, 3},
		ContentsMode:  netsec.ModeAES256XTS,
		FilenamesMode: netsec.ModeAES256HSH,
		Flags:         netsec.PolicyFlagDirectKey,
	}

	_ = fs.Mkdir("/secure")
	_ = fs.SetPolicy("/secure", policy)
	_ = fs.Create("/secure/secret")

	child, err := fs.PolicyAt("/secure/secret")
	fmt.Println(child.Equal(policy), err) // true <nil>
}
```

跨目录操作通过 `ErrPolicyMismatch`、`ErrPlaintextIntoEncrypted` 和
`ErrEncryptedIntoPlaintext` 报告拒绝原因，可用 `errors.Is` 判断。
