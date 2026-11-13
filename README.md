# SBAT世代吊销与节解析

shim 在 Secure Boot 下用 SBAT 世代号吊销有漏洞的引导组件，每个可执行文件在 .sbat 节里带组件名与世代号，攻击者拿旧版已签名的引导程序回退，或让 .sbat 节缺失、字符串带多余字段绕过比较。为 Go 引导准入服务写供镜像审批调用的 SBAT 校验包：输入 PE 镜像节表（名称、虚拟地址、原始大小）、.sbat 节内容和已登记的吊销策略（组件名到最低世代号）；.sbat 为 CSV，首行以 sbat,1 开头，其后每行为 组件名,世代,厂商信息；世代不是十进制整数、组件名重复、节缺失、原始大小与实际内容长度不符都拒绝；任一组件世代低于最低值拒绝；策略更新只能升高世代号，不得降低，已被吊销的世代不能因策略回滚而恢复。用旧世代回退、缺节、重复组件、非数字世代和策略回滚验证。

## `sbat` 包

镜像审批入口使用 `PolicyStore.ValidateImage`，内部依次完成节表核对、CSV 解析与吊销比对：

```go
import "example.org/netsec/q0660/sbat"

store := sbat.NewPolicyStore(map[string]uint64{"shim": 2, "grub": 3})

// 审批镜像：sections 来自 PE 节表，content 为 .sbat 节原始字节。
err := store.ValidateImage(sections, sbatContent)
```

拒绝条件与可判定的哨兵错误：

- 节表无 `.sbat`：`ErrSectionMissing`
- `RawSize` 与实际内容长度不一致：`ErrSizeMismatch`
- 首行不是 `sbat,1`、空行、字段缺失、组件名含空白：`ErrMalformed`
- 世代不是无符号十进制整数（含符号、空格、溢出）：`ErrInvalidGeneration`
- 组件名重复：`ErrDuplicateComponent`
- 组件世代低于策略最低值：`*RevocationError`（用 `errors.As` 判定，含组件名、实际世代、最低世代）

策略更新通过 `PolicyStore.Update` 合并新版本：只接受新组件或不低于现值的世代；
降低任一组件、删除已登记组件都会返回 `ErrPolicyRollback` 且不改动任何已存值，
因此重放旧策略无法恢复已被吊销的世代。无状态场景可直接调用
`sbat.CheckSection(sections, content, policy)`。

运行测试：`go test -race ./sbat/`。
