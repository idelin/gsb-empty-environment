# 多方随机数承诺防替换

`example.org/netsec/q-115` 是一个可导入的 Go 承诺/揭示组件。它把一个轮次的参与者集合在开始时冻结（最多 32 人），每人只能绑定一份承诺和一份揭示，并在全部揭示验证后产生确定性结果。

## 协议

参与者 `p` 在轮次 `r` 的承诺为：

```text
SHA256(uint32be(len(r)) || r || uint32be(len(p)) || p || nonce)
```

- 长度均为字节数，使用 32 位大端前缀；`nonce` 固定 32 字节。
- 轮次 ID 在同一个宿主日志的生命周期内不可复用，因此旧轮次的完整消息不能搬到新轮次。
- 每个参与者只接受第一份承诺和第一份揭示。完全相同的重传幂等成功，不同值被拒绝。
- 全部承诺到齐后才进入揭示阶段。提前揭示被拒绝，不消耗揭示名额。
- 全部揭示验证通过后，结果为按参与者 ID 字节序排序、直接拼接各自 32 字节随机数后的 `SHA256`。
- 截止时刻为 `now >= Deadline`；缺任一揭示即整轮 `aborted`。中止轮次永远没有结果，绝不以零补齐缺失随机数。

## 使用

```go
journal, err := netsec.OpenFileJournal("lottery.wal")
if err != nil {
    return err
}
defer journal.Close()

host, err := netsec.NewHost(journal) // 重放已有事件
if err != nil {
    return err
}

roundID := "round-2026-11-17"
deadline := time.Now().Add(30 * time.Second) // time.Now 带单调时钟读数
err = host.StartRound(netsec.RoundConfig{
    ID:           roundID,
    Participants: []string{"alice", "bob", "carol"},
    Deadline:     deadline,
}, time.Now())

nonce, err := netsec.NewNonce()
commitment, err := netsec.Commitment(roundID, "alice", nonce[:])
err = host.Commit(roundID, "alice", commitment[:], time.Now())
err = host.Reveal(roundID, "alice", nonce[:], time.Now())

result, err := host.Result(roundID)
```

宿主负责计时。每次调用都传入宿主单调时钟的读数（生产环境用 `time.Now()`，测试可注入时间），组件本身不读墙钟。宿主应周期性调用 `Timeout(roundID, now)`；即使宿主漏掉定时器，任何在截止时刻之后到达的 `Commit`/`Reveal` 也会先持久化中止事件，再返回 `ErrDeadlineExceeded`。

## 持久化与确认

- `Host` 的每次状态迁移都由 `Journal` 中的一个事件驱动：轮次开始、承诺、揭示或超时。
- `Journal.Append` 返回成功表示事件已持久化（`FileJournal` 每次写入后执行 `fsync`）。只有落盘成功后才更新内存状态并向调用方确认。
- 写入或同步失败时状态不变；`FileJournal` 会尽量回滚到追加前长度。重启时 `NewHost` 按顺序重放事件，重建相同状态和结果。
- `FileJournal` 使用带长度和 CRC32C 的记录帧。崩溃留下的不完整尾部会在打开或重放时截断；文件中部的损坏会返回 `ErrJournalCorrupt`。
- 日志是单写者文件。日志完整性依赖本地文件系统及权限，不提供对拥有日志写权限的攻击者的防篡改能力。日志包含承诺和已揭示的 32 字节随机数，应限制本地读权限。

## 安全边界：最后参与者可以中止

**本协议不能防止最后揭示的参与者选择中止。** 该参与者已经看到其他承诺（可能还有已揭示的随机数），但仍可选择不揭示自己的随机数，使整轮因缺揭示而中止。任何其他参与者在截止前拒绝揭示也会造成同样结果。

组件能保证的是：这种中止会被检测、持久化并明确标记为 `aborted`，不会伪造或零填充结果；它不能保证随机数一定可用。要阻止选择性中止，需要本包之外的机制，例如时间锁加密、门限方案、经济惩罚或可信第三方。

在至少一个参与者诚实且不中止的前提下，32 字节随机数承诺隐藏了非零熵，外部攻击者不能通过重复、跨轮重放、并发替换或迟到揭示改变已验证的结果。

调用方还必须认证消息确实来自其声明的参与者 ID。若传输层没有身份认证，攻击者可以抢先替其他参与者提交承诺，使对方无法再提交自己的承诺。该问题不属于承诺格式或状态机能够解决的范围。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖截止时刻前后与精确边界、重启后的重复消息、跨轮搬运、抢先揭示、并发承诺替换、并发揭示终结、超时与最终揭示竞争，以及持久化失败不改变状态。
