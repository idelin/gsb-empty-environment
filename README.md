# 不可变路径复制字节前缀树（Go，仅标准库）

磁盘上的内容寻址（SHA-256）持久化前缀树。每个树节点是不可变文件，
文件名即其内容哈希；写入时只复制根到目标叶子这一条路径，兄弟子树和
历史根的引用永远有效。仅依赖 Go 标准库，测试基于 `go test`。

## 快速开始

```sh
go test -v -count=1 .          # 全部测试
go run ./cmd/triedemo          # 运行样例（默认目录 $TMPDIR/triedemo，-dir 指定）
```

数据目录由调用方指定（`Open(dir)`），目录布局：

```
<dir>/
  roots.json          # 清单：当前根、序列号、已打标签快照（原子替换）
  roots.json.bak      # 清单备份（提交后主文件损坏时回退，可能落后一个版本）
  nodes/<xx>/<64位十六进制SHA-256>   # 内容寻址节点，按首字节分 256 片
```

## 特性与验收点

- **路径复制**：`Put/Delete` 只分配根到被改叶子路径上的新节点；更新深度为
  N 的键只写 N+1 个节点，未触及的子树共享原引用（`TestPathCopySharing`
  直接比较改动前后兄弟子树的节点引用）。
- **删 `ab` 不删 `abc`**：删除压缩只剪除"无值且无出边"的节点；`b` 仍有
  到 `c` 的出边，因此不被剪除（`TestDeletePrefixDoesNotDeleteChildren`）。
- **历史根可读旧值**：`Put/Delete` 返回新根引用，`ViewAt(root)` 可在任意
  历史版本上读取；`Snapshot(label, root)` 给根打标签，防止被 GC 回收，
  重启后仍在（`TestSnapshotHistoryAcrossRestart`）。
- **删除压缩 + GC**：删除时稀疏 `children` 收缩、空叶子消失；
  `Compact()` 从当前根和全部快照做可达性标记，只删除无主节点文件。
- **空键与二进制键**：键是任意 `[]byte`，空键 `nil/[]byte{}` 的值存于根
  节点；扫描按严格字节字典序，空键排最前（`TestPutGetEmptyAndBinaryKeys`、
  `TestPrefixScanOrder`）。
- **前缀扫描**：`ScanPrefix(prefix, fn)` 按字典序回调，返回 error 可中止。
- **引用可验证、损坏可定位**：加载节点时重算 SHA-256 并与文件名/父节点
  中的引用比对。损坏或缺失时返回 `*VerifyError`，内含**损坏节点引用、
  父节点引用、边标签**，例如：

  ```
  trie: 引用校验失败: 父 5fe915093788 经边 0x62 引用的子节点
  be22d76c2498…d4729: 内容哈希与引用不一致（节点文件损坏）
  （实际内容哈希 28108fa3ddf0…）
  ```

  `Verify()` 绕过内存缓存强制重读全部可达节点，适合外部损坏后的巡检
  （`TestCorruptedReferenceIsLocated` / `TestDanglingReferenceIsLocated` /
  `TestTamperedRootFailsVerification`）。

## 磁盘节点格式

```
magic "IMTN" | version u8 | flags u8(bit0=有值) | valLen u32-le | value
            | nChild u16-le | (tag u8, childID [32]byte) × nChild
```

子边按标签字节升序排列；节点 ID = `SHA256(编码内容)`。引用本身即完整性
承诺：任何位翻转都会让哈希失配。零 ID（32 个 0）表示空树，不落文件。

## 提交协议与故障注入

一次写事务按顺序执行：

1. 在内存中做路径复制，得到新根与一批新节点；
2. 每个节点 `写临时文件 → fsync → rename`（`BeforeNodeRename` 注入点）；
3. `fsync` 新建分片目录与分片目录父目录（`BeforeNodeDirSync`）；
4. `roots.json` 同样临时文件 + fsync + rename（`BeforeRootsWrite`/
   `BeforeRootsRename`）——**rename 完成即提交点**；
5. `fsync` 数据目录，使根文件的目录项持久（`BeforeRootsDirSync`）；
6. 更新 `roots.json.bak`（`BeforeRootsBackup`）；
7. 发布内存状态（`AfterCommit`）。

`SetFaultHooks(FaultHooks{...})` 在每个原子步骤前后插入钩子，返回
`ErrInjected` 即模拟"进程此刻被杀"。`TestInjectedCrashRecovery` 对全部
8 个注入点逐一验证：提交点前注入 → 内存与磁盘都停留在旧根；提交点后
注入 → 重开后新根生效。任何阶段崩溃都不会留下半写文件（临时文件在下次
`Open` 时清扫，见 `TestOrphanedNodesAfterCrashReclaimed`）。

## 崩溃恢复与断电保证的边界

**保证成立的部分（进程崩溃 / kill -9 / 正常 panic）**

- 所有用户数据通过 `write → fsync(file) → rename → fsync(dir)` 落盘，
  提交点（`roots.json` rename 成功）之后崩溃，重启必定读到完整新树；
  提交点之前崩溃，重启必定读到完整旧树。不存在"半棵树"。
- 节点内容寻址且不可变：崩溃中途留下的节点文件不被任何根引用，只是垃圾，
  由 `Compact()` 回收；它们不可能被误认为有效版本。
- 主清单损坏（如写入中途被杀产生截断文件）时，`Open` 回退到
  `roots.json.bak`（`TestBackupManifestRecovery`）。备份最多落后一个
  已提交版本，这是有意的取舍。

**边界（不提供超出操作系统/硬件的保证）**

1. **fsync 的物理语义依赖平台**：在尊重屏障的本地文件系统 + 真实存储上，
   上述顺序保证断电不丢已提交数据；在网络盘、某些 FUSE、配置了
   `barrier=0` 的文件系统或会谎报 flush 的磁盘上，fsync 不保证落到介质，
   本库无法在其之上提供更强保证。
2. **目录 fsync 窗口**：提交点之后、目录 fsync 之前断电，极端情况下
   rename 的目录项可能丢失，存储回退到上一个根（旧树完好，最新一次提交
   可能不可见）。这正是把提交点之后的 `BeforeRootsDirSync` 作为独立注入
   点、并在测试中区分"逻辑已提交/物理目录项待持久"的原因。
3. **备份不是双活副本**：`roots.json.bak` 只覆盖"主文件损坏但介质完好"；
   若整个目录被外部破坏/加密，需要调用方自行做目录级备份。
4. **校验范围**：`Get/Scan` 只验证被访问路径上的节点（惰性）；全量校验
   需调用 `Verify()`（或 `Compact()`，其先做全量可达性校验再回收）。
   内存缓存中的节点不会被外部篡改感知，巡检请在新进程中进行或调用
   `Verify()`。
5. **GC 与长生命周期句柄**：未打标签的历史根在被新版本取代后，其独占
   节点会在 `Compact()` 时回收；请先 `Snapshot` 再压缩。压缩期间假设
   只有一个写进程（文件锁未实现，多写者需调用方加外部锁）。

## 主要 API

```go
s, _ := trie.Open("/path/to/dir")
newRoot, _ := s.Put([]byte("foo/bar"), []byte("v"))
newRoot, _ = s.Delete([]byte("foo/bar"))
val, err := s.Get([]byte("foo/bar"))
s.ScanPrefix([]byte("foo/"), func(kv trie.KV) error { ... ; return nil })
s.ViewAt(historicalRoot).Get(key)
s.Snapshot("release-1", trie.NodeID{}) // 给当前根打标签
removed, _ := s.Compact()
if err := s.Verify(); err != nil { var ve *trie.VerifyError; errors.As(err, &ve) }
```

## 文件清单

- `node.go`：`NodeID`（SHA-256 引用）、内存节点与二进制编解码
- `store.go`：打开/恢复、清理解析、清单结构、故障钩子
- `write.go`：路径复制 Put/Delete、原子写、提交协议、GC 标记支撑
- `read.go`：加载校验、`VerifyError` 定位、`View`、Get、前缀扫描
- `snapshot.go`：标签快照、`Compact`、`Verify`
- `cmd/triedemo/main.go`：可运行样例
- `trie_basic_test.go`、`trie_fault_test.go`：go test 测试
