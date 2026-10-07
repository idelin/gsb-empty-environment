# encobj — 本地字段级加密对象库

`encobj` 用 Go 标准库（`crypto/aes` + `cipher.NewGCM`，不自行实现任何加密原语）
实现一个单目录、单写进程的加密对象存储。对象由稳定对象 ID、单调提交版本和
`string -> []byte` 字段映射组成，支持字段的新增、修改、删除与只读快照，不连接
任何外部服务。

## 安全边界（务必阅读）

保证范围**仅限**同一持久目录上的进程崩溃恢复：

- 承诺：提交协议的任何持久化边界前后发生进程崩溃，重启后只恢复出旧的或新的
  **完整**对象，绝不出现两个版本的拼接；已分配 nonce 永不复用。
- **不承诺**：整个目录或 nonce 高水位日志被外部回滚、整目录被复制成两个独立
  实例（如备份恢复、VM 快照分叉）之后仍能防重放或防 nonce 复用。这些场景需要
  目录之外的可信单调计数器，超出本库范围。
- 密钥只由调用方（或测试夹具）提供，驻留内存；库绝不把密钥写入对象目录或日志。

## 威胁模型内检测到的篡改

读取任何对象前，先验证 manifest 及全部所需字段，然后一次性交付完整明文：
缺字段、多字段、字段密文互相搬移、旧字段密文替换、manifest 被替换为旧版本、
`current` 索引被回滚，全部导致读取失败，且不会通过回调、返回值或日志暴露部分
明文。旧 manifest 的拒绝依据是本地已提交版本索引（内存索引 + `current` 记录），
不依赖 AEAD 本身推断新旧。

## 快速开始

```go
st, err := encobj.Open(encobj.NewFSStorage(dir), &encobj.Options{
    Keys:      map[string][]byte{"k2026a": key}, // 32 字节 AES-256
    ActiveKey: "k2026a",
})
v, err := st.Commit("user-1", encobj.Operation{
    OpID: "req-9f27",                 // 幂等操作 ID
    Set:  map[string][]byte{"email": []byte("a@b.c")},
    Del:  []string{"nickname"},
})
sn, err := st.Snapshot("user-1")      // 全量验证后一次性交付
defer sn.Close()                      // 释放密钥租约与文件钉住
email, ok := sn.Get("email")
```

## 提交协议

每次 `Commit` 按以下顺序推进，每一步持久化完成后才进入下一步：

1. **nonce 预留**：高水位记录追加到 `nonce.log` 并 fsync；只有上界落盘后才
   可使用区间内 nonce。
2. **字段密文写入**：新增/修改字段逐字段加密（AEAD AD 绑定对象 ID、稳定字段
   ID、字段修订号、schema 版本、key ID），临时文件 + fsync + rename + 目录
   fsync。未变字段共享旧密文，新 manifest 明确承诺其字段修订号。
3. **manifest 发布**：新 manifest 承诺名称→稳定字段 ID 映射、当前字段集合、
   各密文摘要、对象提交版本、幂等操作 ID 与内容摘要，同样以原子替换写入。
4. **索引更新**：原子替换 `obj-*/current`（版本 + manifest 文件名 + 摘要 +
   校验和）。**此点为提交点**：之前崩溃只有旧版本可见，之后只有新版本可见。
5. **幂等日志**：把 `(版本, opID, 内容摘要)` 追加到 `ops.log` 并 fsync。
   同一记录也在 manifest 内，崩溃落在 4–5 之间时由恢复流程从 manifest 补建。
6. **临时文件回收**：不再被当前 manifest 或任何存活快照引用的字段/manifest
   文件被回收；已分配 nonce 永不返还。

幂等语义：同一 `OpID` + 相同内容摘要重试 → 返回原提交版本；同 ID 不同内容 →
`ErrConflict`。

## 密钥轮换

`SetActiveKey` 发布新 key ID 后，新提交只使用新密钥。`RotateAll` /
`RotateObject` 在后台按对象快照重新加密旧对象：候选在对象锁外构建，安装时
在锁内比较仍当前的提交版本与 manifest 摘要，一致才原子替换 `current`；前台
若已提交更新版本，旧候选被丢弃并重试，旧明文绝不会覆盖新版本。轮换不改变
对象版本号。旧读快照持有对应 manifest 与密钥租约，所有引用消失前
`RetireKey` 拒绝退役旧密钥；删除对象与失败轮换的暂存密文会被回收。

## nonce 分配

字段与 manifest 加密共用同一个 96 位单调计数器（持久高水位预留区间）：
重启跳过整个已预留区间，未使用值被浪费但绝不复用；并发线程共享分配器；同一
目录由 flock + 进程内注册表保证只有一个写进程；计数达到 `MaxNonce`（默认
2^62）后拒绝继续加密并返回 `ErrNonceExhausted`。

## 崩溃恢复

启动时：校验并截断 `nonce.log` / `ops.log` 的半写尾部（中段校验损坏拒绝恢
复）；逐对象读取 `current` 索引，验证其指向的 manifest（摘要 + AEAD + 编码）
及全部字段文件存在且可认证，任何缺失或损坏都拒绝启动；清扫 `*.tmp-*` 暂存
文件与未被引用的密文；从 manifest 补建幂等索引。跨对象事务不在保证范围内。

## 存储层语义

所有文件操作封装在 `Storage` 接口（可注入，便于故障注入测试），依赖：

- `WriteFileAtomic`：同目录临时文件写满并 fsync → `rename(2)` 原子替换 →
  目录 fsync；崩溃后目的地要么旧要么新，绝不混合。
- `AppendFile`：追加写并 fsync；崩溃可能留下半写尾部，但已 fsync 的中段
  不会损坏。
- `RemoveFile` + `SyncDir`：删除在目录 fsync 后持久。
- `Lock`：目录级排他 flock。

## 测试

```
go test ./...        # 功能、篡改、故障注入、交错枚举
go test -race ./...  # 竞态检测
```
