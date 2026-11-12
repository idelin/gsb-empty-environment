# io_uring 固定文件权限：SQE 校验包

`netsec` 包供特权 io_uring 代理在替不可信客户端提交 SQE 之前做强制校验。
内核的 fixed file 引用可能比原始 fd 活得更久，linked SQE 又能在槽位之间
跳转，因此「fd 已关闭 / 权限已收回 / 跨凭据串用」必须由代理自行拦截。

## 校验项

- **opcode 白名单**：`Config.AllowedOpcodes`，默认只放行数据面操作
  （read/write/recv/send/splice/tee/fsync/accept/connect 等），未知 opcode
  与 `OPENAT`、`FIXED_FD_INSTALL` 等高危操作默认拒绝。
- **IOSQE flag 掩码**：`Config.AllowedFlags`（默认 0xff），未知 flag 位拒绝；
  并校验 6.12 `IOSQE_FILE_INDEX_UPPER` 与 `file_index_upper` 字节的一致性。
- **fixed file 下标**：要求 fd 类 opcode 必须带 `IOSQE_FIXED_FILE`；下标按
  32/40 位扩展编码解码后做越界检查；no-fd opcode（NOP/TIMEOUT 等）禁止带
  fixed-file 标志。
- **槽位读写权限**：按 opcode 的 read/write 分类核对 `Slot.Access`；
  `SPLICE`/`TEE` 的目标槽要求写、`splice_fd_in` 源槽要求读，且两槽必须同凭据。
- **personality**：`sqe.personality` 必须等于槽位凭据快照中的
  `PersonalityID`（splice/tee 与 `splice_fd_in` 共用 union，无独立
  personality，改由双槽同 `CredID` 约束）。
- **链上后续 SQE**：`IOSQE_IO_LINK`/`IOSQE_IO_HARDLINK` 组成的链内不得切换
  `CredID` 或 personality，尾元素带 link 标志按 dangling link 拒绝；整批基于
  同一张表快照校验，杜绝链内 TOCTOU。
- **撤销粘性**：`SlotTable.Revoke` 把槽位置为 `SlotRevoked`，之后同一槽位
  一律拒绝，只有带新 generation 的 `RegisterSlot` 才能重新启用。

## 用法

```go
tab := netsec.NewSlotTable(64)
tab.RegisterSlot(0, netsec.AccessRead, netsec.Credential{
    CredID: 1001, PersonalityID: 11,
}, "client-a:input")

v := netsec.New(tab, netsec.DefaultConfig())

sqe, err := netsec.ParseSQE(raw64)         // raw64 为客户端的 64 字节 SQE
if err != nil { /* RuleError: RuleSQESize */ }

if _, err := v.ValidateOne(sqe); err != nil {
    var re *netsec.RuleError
    errors.As(err, &re)
    // re.Rule 标识被触发的规则，errors.Is(err, netsec.ErrSlotAccessDenied) 等
}

// 提交批次（含 linked SQE）时走批量入口
decisions, err := v.ValidateBatch(parsedBatch)

// fd 关闭或权限收回时
tab.Revoke(0)
```

## 测试覆盖

`go test ./...`（含 `-race`）覆盖题目要求的四个攻击场景及边界：

- 越界槽位（普通下标、40 位扩展下标、负数下标）
- 只读槽写入 / 只写槽读取
- 链内换槽（跨 `CredID`、链内换 personality、dangling link）
- 撤销后同一槽位重复重用，以及重新注册后恢复
- opcode/flag 白名单、personality 不匹配、splice 双槽、快照原子性与并发撤销
