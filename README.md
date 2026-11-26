# credentialauth

`credentialauth` 是 Go 凭据认证宿主的离线合并与全局消费证明包。它把一个用户在多台安全密钥设备上产生的凭据状态收据、被截断的已确认批摘要日志、撤销前保留凭据清单合并成一个固定的全局消费证明。

模块路径：

```go
import credentialauth "formal.local/w3b-6ca8b0126c"
```

## 核心规则

- 最多接受 `credentialauth.MaxDevices`（3）个不同 `deviceID`。
- 凭据身份严格由 `(rpID, credentialID)` 组成；不同 RP 下的同名 ID 互不影响。
- 同一 `(rpID, credentialID)` 出现在多台设备上时视为同一逻辑凭据，全局容量为 1。
- 每个 `BatchSummary` 只保留候选凭据集合和事务数。合并器需要为每个事务槽选择不同凭据，并使所有批次同时可行；仅检查单批 Hall 条件不够。
- `Reservation` 中的凭据永久视为已锁定，不能用于满足已确认批次，也不能签发新许可。
- 设备收据必须满足启动代次与签名计数器的单调性。相同代次、相同计数器出现不同事务摘要时判定为克隆分叉；新代次计数器低于历史计数器时判定为计数器倒退。
- 批次候选必须能追溯到已签设备收据。凭据允许用途与批次所需用途相交为空时，该凭据不可用于该批次。

签名本身由宿主使用设备公钥验证；本包不持有公钥，也不替代 FIDO2/WebAuthn 签名验证。收据内容会绑定进证明摘要。

## 快速使用

```go
snapshot := credentialauth.Snapshot{
    Devices: []credentialauth.DeviceReceipt{
        {
            DeviceID:         "device-1",
            BootGeneration:   4,
            SignatureCounter: 91,
            Credentials: []credentialauth.CredentialRef{
                {RPID: "example.com", CredentialID: "cred-a"},
                {RPID: "example.com", CredentialID: "cred-b"},
            },
        },
    },
    Batches: []credentialauth.BatchSummary{
        {
            BatchID: "batch-7",
            RPID:    "example.com",
            CandidateCredentials: []credentialauth.CredentialRef{
                {RPID: "example.com", CredentialID: "cred-a"},
                {RPID: "example.com", CredentialID: "cred-b"},
            },
            TransactionCount: 1,
        },
    },
    Reservations: []credentialauth.Reservation{
        {RPID: "example.com", CredentialID: "cred-c"},
    },
    Request: &credentialauth.PermitRequest{
        RPID:              "example.com",
        TransactionDigest: "tx-new",
        Purpose:           "sign",
        Candidates: []credentialauth.CredentialRef{
            {RPID: "example.com", CredentialID: "cred-b"},
        },
    },
}

proof, err := credentialauth.BuildProof(snapshot)
if err != nil {
    // ErrNoGlobalAssignment, ErrCandidatesExhausted, ErrDeviceFork, ...
}
_ = proof.Permit.LockID
_ = proof.Assignment
_ = proof.Locked
```

对于需要进程内幂等签发的宿主，使用 `Verifier`：

```go
verifier := credentialauth.NewVerifier()
first, err := verifier.VerifyAndIssue(snapshot, request)
second, err := verifier.VerifyAndIssue(snapshot, request)
// first.Permit.LockID == second.Permit.LockID
```

相同快照和相同请求返回同一个锁定证明。相同快照但不同请求返回 `ErrSnapshotConflict`，不会签发第二份互斥凭据。`Verifier` 的状态是进程内状态；跨进程持久化幂等性应由宿主持久化证明并按其摘要去重。

## 固定补全与全历史安全

默认模式 `SafetyFixedCompletion` 把截断日志固定为一个确定性的完整分配。只要存在某个完整补全不使用请求候选，就可以签发许可，证明中的 `Assignment` 会固定该补全。

需要“在该快照的任意可能补全中都未被消费”的更强保证时，设置：

```go
request.Mode = credentialauth.SafetyUniversal
// 或 request.RequireUniversal = true
```

此时只有 `ConditionallyUsed == false` 的候选可用。证明的 `Statuses` 同时给出：

- `ConditionallyUsed`：至少一个相容补全能消费它。
- `Unavoidable`：每个相容补全都会消费它。
- `UniversallyFree`：任何相容补全都不会消费它。
- `Reserved`：撤销前保留锁。
- `Available`：按当前安全模式可用于新许可。

## 公开 API

- `BuildProof` / `Prove` / `Merge` / `MergeAndProve`：校验并生成确定性证明。
- `VerifyAndIssue` / `IssuePermit`：校验并签发一个固定许可。
- `Analyze`：只校验和计算状态，不签发许可。
- `CheckProof` / `ValidateProof` / `(*Proof).Verify`：核验证明是否对应该快照。
- `Oracle` / `BruteForce` / `EnumerateAssignments`：小规模全匹配枚举 oracle，用于与多项式匹配路径交叉核验。
- `Verifier`：并发安全、幂等的进程内签发门。

## 测试

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖跨批全局 Hall 失败、保留凭据、RP 隔离、设备分叉、计数器倒退、固定/全历史模式、幂等签发，以及随机小规模实例上的 oracle 一致性。
