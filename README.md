# TUF回滚与冻结防护

软件更新客户端会遇到镜像回放旧元数据（回滚）或停发新元数据（冻结）。为Go更新库实现供下载流程调用的 TUF 元数据校验包：按 root、timestamp、snapshot、targets 的顺序，各角色签名门限由受信 root 指定；新 root 须同时被旧 root 与自身的密钥集达到门限，版本号恰好加 1；timestamp、snapshot、targets 的版本不得小于已存版本，snapshot 记录的 targets 版本及哈希须与实际一致，过期时间早于注入时钟的元数据拒绝；root 轮换后须丢弃旧的 timestamp 与 snapshot。用版本回退、过期冻结、只被新密钥签的 root 和合法轮换验证。

## 设计与 API

仅依赖 Go 标准库（`crypto/ed25519` 签名、SHA-256 哈希），无外部依赖。

- `types.go`：元数据结构（`Root`/`Timestamp`/`Snapshot`/`Targets`）、确定性
  canonical JSON、key ID 计算、门限签名校验与哈希/长度校验。
- `store.go`：`Store` 持有当前受信元数据，所有方法互斥；`Clock` 为注入时钟，
  生产环境传 `time.Now`，测试注入可控时钟以复现冻结。

下载流程按以下顺序调用：

1. `NewStore(rootJSON, clock)`：用带外获取的初始 root 建立信任锚（结构校验 +
   过期校验，初始 root 不做自签名校验）。
2. `UpdateRoot`：root 版本必须恰好 +1；分别用旧 root 与新 root 各自的 root
   角色密钥集和门限校验签名；成功后丢弃缓存的 timestamp 与 snapshot。
3. `UpdateTimestamp` → `UpdateSnapshot` → `UpdateTargets`：每个角色先做过期
   校验、版本不回退（相等版本视为刷新）、再按受信 root 的门限验签；timestamp
   固定 snapshot 的版本与哈希，snapshot 固定 targets 的版本与哈希。
4. `VerifyTarget(name, data)`：用 targets 中记录的哈希/长度校验下载文件。

错误以哨兵值返回，调用方用 `errors.Is` 判定，例如 `ErrRollback`、`ErrExpired`、
`ErrSignature`、`ErrHash`、`ErrVersion`、`ErrUnavailable`。

## 校验覆盖的攻击面

- 回滚：root 必须严格 +1；timestamp/snapshot/targets 版本不得低于已存版本；
  跨文件固定的版本不一致同样按回滚拒绝。
- 冻结/过期：`expires` 不晚于注入时钟即拒绝（含恰好相等），所有角色一致。
- 未授权轮换：仅被新密钥（或仅被旧密钥）签名的新 root 均无法满足双向门限。
- 内容篡改：timestamp→snapshot、snapshot→targets 的哈希/长度固定，目标文件
  哈希固定，任何字节改动都会被拒绝。

## 测试

```sh
go test -race -count=1 ./...
```

`netsec_test.go` 覆盖正常链路、版本回退、过期冻结（含时钟前推）、只被新密钥
签名的 root 拒绝、合法轮换后丢弃 timestamp/snapshot、门限与未授权密钥、哈希与
版本固定、以及顺序约束。
