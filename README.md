# atomicpub — 多文件原子发布工具

读者只能看到**完整旧版**或**完整新版**，永远不会读到混合版本。

## 目录设计

```
root/
  CURRENT              指针文件：当前生效版本目录名（原子切换）
  v-000001/            已提交版本：数据文件 + MANIFEST.json（校验和清单）
  v-000002/
  tmp-v-000003-a1b2/   发布途中的暂存目录（恢复时识别并清理）
```

## 发布流程（先校验，再提交，最后切换指针）

1. 所有数据文件写入 `tmp-*` 暂存目录（逐文件 fsync）；
2. **校验**：重读每个暂存文件并核对 SHA-256，不合格即中止，旧版本不受影响；
3. 写入 `MANIFEST.json`（版本名 + 每个文件的 size/SHA-256），fsync 目录；
4. **提交**：`rename(tmp-*, v-NNNNNN)` 原子改名，fsync 根目录；
5. **发布**：写 `CURRENT.tmp` 后 `rename` 到 `CURRENT` 原子切换指针，fsync 根目录。

版本只有在指针切换后才对读者可见，而切换是单次原子 rename，
因此读者要么拿到旧版本目录、要么拿到新版本目录，绝不混合。

## 读者语义

- `Acquire()` 返回钉住某一版本的读句柄 `Reader`；
- 新版本发布后，**旧读句柄继续可用**（按引用计数防止被 GC）；
- 每次 `Read` 都按清单校验 SHA-256，撕裂内容直接报错而不是返回脏数据。

## 恢复（Open 时执行）

- `tmp-*` 暂存目录：已知布局的中断发布残留 → 删除；
- `CURRENT.tmp`：中断的指针切换残留 → 删除；
- `v-*` 目录：逐文件校验清单；损坏的**保留在磁盘、绝不使用、绝不删除**，记入报告；
- **未知条目绝不删除**，只记入 `Report.UnknownEntries`；
- 指针缺失 / 无法解析 / 悬空（指向缺失或损坏版本）→ 修复为最新有效版本；
- 已提交但指针未切换的版本（崩溃残留）**不会**因恢复而变成生效版本，
  在下一次 `Publish` 前由 sweep 回收。

## 垃圾回收

- `gc`（发布后与读句柄关闭时）：只回收编号**严格小于**当前版本且无读者的
  已提交版本——在途发布已提交但未切换指针的版本编号更大，天然受保护；
- `sweep`（下次发布前、持发布锁）：清理暂存目录与崩溃残留的已提交版本。

## 故障注入测试

`FaultFS` 在每个文件系统操作前执行钩子，可注入：

- 写失败（`TestWriteFailureDuringPublish`、写入比特翻转被校验捕获
  `TestCorruptWriteCaughtByValidation`）；
- 重命名失败（`TestCommitRenameFailure`）；
- 重启（`ErrCrash`，含提交后指针切换前崩溃
  `TestCrashBetweenCommitAndPointerFlip`、逐步注入每一个写操作
  `TestCrashAtEveryStep`、恢复过程中崩溃
  `TestCrashDuringRecoveryPointerRepair`）；
- 坏指针修复（`TestCorruptPointerRepaired`、`TestDanglingPointerRepaired`）；
- 未知目录保留（`TestUnknownEntriesNeverDeleted`）、不完整版本保留但不启用
  （`TestIncompleteVersionKeptButNotUsed`）；
- 并发读写压力测试核对读者从未读到混合版本
  （`TestConcurrentReadersNeverMixed`，`-race` 通过）。

## 使用

```go
s, _ := atomicpub.Open("/var/lib/mydata", nil)
s.Publish([]atomicpub.File{{Name: "a.txt", Data: a}, {Name: "b.txt", Data: b}})

r, _ := s.Acquire()
defer r.Close()
data, _ := r.Read("a.txt") // 始终来自同一完整版本
```

运行测试：`go test -race ./...`
