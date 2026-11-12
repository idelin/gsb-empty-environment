# 预接收钩子签名与额度

攻击者推送含未签名提交的合并，或借大量引用耗尽仓库额度。为Go Git托管服务的pre-receive钩子实现可导入的准入包：对每条ref更新，遍历new可达而old不可达的提交，全部须带许可签名者的有效签名，按提交图而非首父线检查；并发推送原子预留对象字节额度，拒绝后归还。用旁支未签名提交、同时推送和超额样例测试，签名核验由回调提供。

## 设计

包名 `netsec`，仅依赖标准库。核心类型：

- `Repository` / `Commit`：托管侧实现的对象库抽象，`Commit.Parents()` 返回全部父提交（含合并的第二父），`Size()` 返回计入额度的对象字节。
- `VerifySignature`：由钩子宿主提供的签名核验回调，返回签名者身份；未签名或验签失败返回错误，本包不绑定具体 OpenPGP/SSH 实现。
- `Policy.Admit(ctx, updates)`：对一次推送（可含多条 ref 更新）执行准入。
- `Quota` / `Grant`：按仓库共享、并发安全的对象字节额度与预留句柄。

### 提交图遍历（非首父线）

对每条非删除更新，先把 `Old` 可达的全部提交标记为已接受切点（cut），再从 `New` 沿**所有父提交**做 DFS，遇到切点即停止。因此合并提交第二父上的旁支未签名提交也会被检查。多条 ref 引入的同一提交只核验、只计费一次；删除 ref 不引入对象，直接跳过。强制推送（old 非 new 祖先）同样只检查 old 不可达的部分。

### 签名准入

每个新引入的提交都调用 `VerifySignature`：回调报错按 `ErrUnsignedCommit` 拒绝；签名有效但签名者不在 `AllowedSigners` 中按 `ErrSignerNotAllowed` 拒绝。缺失对象以 `ErrObjectNotFound` 失败关闭（fail closed），不会被静默跳过。

### 原子额度与归还

`Admit` 先汇总本次新引入提交的总字节，再通过 `Quota.reserve` 在同一把锁内完成“检查余量 + 预留”，并发推送不可能超额超订。预留成功后若签名核验失败，或预留本身因额度不足失败，都会保证额度回到操作前状态（拒绝即归还）。成功时返回 `Grant`：对象落库后调用 `Commit()` 固化，推送中止调用 `Release()` 归还；二者均幂等。

## 接入示例

```go
quota := netsec.NewQuota(repoByteLimit)                 // 每仓库一个，跨推送共享
policy := netsec.NewPolicy(gitRepo, quota, verifyFn,    // verifyFn 为宿主的验签回调
    "alice@example.com")

grant, err := policy.Admit(ctx, refUpdates)             // refUpdates 来自 pre-receive 的 stdin
if err != nil {
    // err 可用 errors.Is 区分 ErrUnsignedCommit / ErrSignerNotAllowed /
    // ErrQuotaExhausted / ErrObjectNotFound；拒绝时额度已自动归还
    reject(err)
    return
}
defer grant.Release()           // 落库失败时归还；成功路径用 Commit 固化
if err := applyPush(); err != nil {
    return
}
grant.Commit()
```

## 测试

- `TestAdmitRejectsUnsignedSideBranch`：合并第二父上的未签名旁支提交被拒（首父线检查会漏过）。
- `TestAdmitConcurrentPushesReserveAtomically`：60 个并发推送竞争同一额度，断言全程 `used <= limit`、预留与账目精确平衡、全部归还后归零（`go test -race` 无竞态）。
- `TestAdmitRejectsOverQuotaAndReturnsBytes`：超额推送被拒且无部分预留，归还后可重试。
- 另有非法签名者、验签失败、多 ref 去重、ref 删除、强制推送切点、缺失对象失败关闭等用例。

```sh
go test -race -count=1 ./...
```
