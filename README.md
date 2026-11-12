# btrfs send stream validation

Package `netsec` validates a btrfs send stream before the receive service
parses or replays it.

```go
child := netsec.MustParseUUID("11111111-2222-3333-4444-555555555555")
parent := netsec.MustParseUUID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

report, err := netsec.ValidateStream(stream, netsec.IncrementalStream(child, parent))
if err != nil {
    return fmt.Errorf("reject send stream: %w", err)
}
_ = report
```

`FullStream(uuid)` checks the stream's child UUID and does not constrain a
snapshot parent. Incremental streams use `IncrementalStream(child, parent)` and
require `SNAPSHOT` to carry both the expected child (`A_UUID`) and parent
(`A_CLONE_UUID`) UUIDs. With an all-zero `ExpectedUUID` and a nil `ParentUUID`,
UUID checks are disabled.

The validator reads the 17-byte stream header, every command frame and every
TLV, verifies the btrfs raw CRC32C (seed 0, no final inversion) over the
command header with its CRC field zeroed plus the command data, and rejects:

- command data larger than 16 MiB;
- TLVs whose declared length is larger than the frame;
- malformed or duplicate TLVs;
- command types unknown to protocol version 1.

For interoperability with third-party stream writers, the first command selects
between the btrfs raw convention and conventional CRC32C, with either a
full-frame or payload-only checksum; the selected convention is then enforced
for every remaining command.

Paths on `MKFILE`, `MKDIR`, `SYMLINK`, `RENAME`, `LINK`, `UNLINK`, `WRITE`,
`CLONE` (and the other v1 path-bearing commands) must be relative, must not
resolve outside the subvolume root, and must not traverse a symlink created
earlier in the same stream. Symlink state is updated when a symlink is renamed,
unlinked, or moved as part of a directory rename. Absolute paths, escaping
`..` components, NUL bytes, and symlink traversal are rejected before the
caller's parser sees the command.

btrfs send 产生的流会被接收端重放成文件系统操作，攻击者提供的流可含越出目标子卷的路径、指向目标外的符号链接，或先创建符号链接再写入其下的路径。为 Go 备份接收服务写供流解析与应用前调用的校验包，按命令帧解析（帧头 4 字节长度、2 字节命令、4 字节 CRC32C，其后是 2 字节类型加 2 字节长度的属性）：流头为魔数 btrfs-stream 加终止零及 4 字节版本；MKFILE、MKDIR、SYMLINK、RENAME、LINK、UNLINK、WRITE、CLONE 的 path 须相对子卷根，规范化后不含 .. 且不经过本流内先前创建的符号链接；SUBVOL 与 SNAPSHOT 的 UUID 须符合调用方期望的父子关系；CRC 错误、属性长度超过帧长、帧长超过 16 MiB 拒绝。用 .. 路径、符号链接后写入、CRC 错误和合法增量流验证。
