# 多方随机数承诺防替换

`netsec` 是一个可导入的 Go 承诺揭示组件，用于最多 32 个固定参与者的抽签/随机数轮次。

## 协议

轮次开始时冻结参与者集合（去重后按 ID 排序保存），轮次 ID 不可复用。

```text
commitment = SHA256(
    be64(len(round)) || round ||
    be64(len(participant)) || participant ||
    nonce
)
```

- `be64` 是 8 字节大端无符号整数长度前缀，长度按 ID 的 UTF-8 字节数计算。
- `nonce` 固定 32 字节，可用 `NewNonce` 从 `crypto/rand` 生成。
- 每个参与者只接受第一份承诺和第一份有效揭示；相同消息重发返回 `Duplicate`，不会写入第二条记录；不同值返回 `ErrCommitConflict` 或 `ErrRevealConflict`，先到者生效。
- 所有参与者都提交承诺后才进入揭示阶段。提交阶段不足人数、或揭示阶段缺少/存在无效揭示，都会在截止时刻整轮中止。
- 全部揭示验证通过后，结果为按参与者 ID 排序、直接拼接 32 字节随机数后的 SHA256。不存在零补齐或部分结果。

截止时刻使用“到达即迟到”的语义：事件时间 `now >= deadline` 被拒绝；宿主定时器用 `Tick`，或显式用 `Advance(monotonicNow)` 触发中止。超时是宿主单调时钟驱动的，默认 `time.Now` 带 Go 单调读数。

## 持久化与回放

`Engine` 把每个被接受的外部事件追加到 journal，并在 `Store.Save` 成功返回后才更新内存、向调用方确认：

- `StartRound` 写入冻结的参与者、开始时间和两个超时；
- `Commit` 写入已接受的承诺；
- `Reveal` 写入已验证的随机数；最后一份揭示使结果可确定；
- `Advance`/`Tick` 写入本次超时中止的轮次。

`Store.Save` 必须原子替换旧内容；返回错误时旧内容必须保持完整。内置 `FileStore` 使用同目录临时文件、文件 `fsync`、原子 `rename` 和目录 `fsync`（尽力而为）。写入失败时当前调用的内存状态不变，调用方可原样重试。`New` 会回放 journal；重复记录按幂等处理，冲突记录或非法记录会拒绝启动，避免静默跳过被篡改的历史。

注意：跨进程重启后，绝对截止时刻按 journal 中的 Unix 时间恢复。生产宿主若需要抵抗系统墙钟回拨，应使用可跨重启的单调时基调用 `Advance`，并按 `RoundView` 中的截止时刻调度。

## 使用

```go
store := netsec.NewFileStore("/var/lib/draw/rounds.jsonl")
engine, err := netsec.New(store) // 默认 time.Now；可用 netsec.WithClock 注入宿主时钟
if err != nil {
    return err
}

roundID := "2026-11-15/42"
_, _, err = engine.StartRound(netsec.RoundConfig{
    RoundID:       roundID,
    Participants:  []string{"alice", "bob", "carol"},
    CommitTimeout: 30 * time.Second,
    RevealTimeout: 30 * time.Second,
})

nonce, err := netsec.NewNonce()
if err != nil {
    return err
}
commitment := netsec.CommitmentFor(roundID, "alice", nonce)
_, err = engine.Commit(roundID, "alice", commitment)
// 所有承诺齐备后：
_, err = engine.Reveal(roundID, "alice", nonce)

// 宿主单调定时器定期调用：
aborted, err := engine.Tick()
```

`Round`/`Rounds` 返回只读快照；只有 `PhaseFinalized` 且 `HasResult` 为真时 `Result` 有效。

## 安全边界

本协议**不能防止最后参与者选择中止**。任一参与者都可以拒绝提交承诺，或在承诺后拒绝揭示有效随机数，直到截止时刻触发整轮中止。固定参与者集合且无第三方的情况下，协议无法区分“参与者掉线”和“参与者故意中止”；宿主只能决定是否用新的轮次 ID 重试。

其他边界：

- 跨轮搬运的承诺摘要即使被写入目标轮次，也无法用原随机数通过揭示验证，因为承诺绑定轮次和参与者；但它可能抢占目标轮次中同名参与者的“第一份承诺”名额。宿主必须认证消息发送者确实是该参与者，本包不提供身份层。
- 重复消息是幂等的，但一份非法揭示不会占用揭示名额；参与者可以在截止前重发正确揭示。
- journal 完整性是安全前提。应限制文件权限，并对回放失败、存储损坏采取 fail-closed 处理。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖承诺编码、截止时刻前后、缺揭示中止且无零填充、恢复后的重复消息、抢先/非法揭示、跨轮搬运、并发替换承诺、并发终结、写入失败不改变状态和文件持久化回放。
