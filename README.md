# kvlog

键目录 + 追加式数据日志的原型。核心纪律：**数据日志是唯一事实源**，键目录（内存及磁盘快照）只是提示。数据追加与目录更新可能只完成其一，因此：

- 每条记录自带 `magic / gen / version / key / value / crc32`，任何偏移在信任前都可校验。
- 追加先写日志并 `Sync`，再更新目录；崩溃只丢目录，重启重建找回。
- 重建从头扫描日志，遇到不完整/损坏的尾项即截断忽略。
- 目录命中必须校验记录代次、键、版本，且版本须为最新；不符则回退日志扫描，绝不误读。
- 压实写入 `data.log.new`（代次 +1），`rename` 原子切换；旧代次的偏移与快照因代次/版本不匹配被整体拒绝。

## 文件

- `file.go` / `osfs.go` / `memfs.go` — 可注入文件模型；`MemFS.Crash()` 丢弃未 `Sync` 的写入，模拟断电
- `record.go` — 记录编解码与校验
- `dirfile.go` — 目录快照编解码（magic + crc，损坏即弃）
- `store.go` — Open/Append/Get/Compact/PersistDir
- `store_test.go` — 崩溃场景枚举：撕裂尾项、垃圾尾项、目录更新丢失、快照过期、目录损坏、压实 rename 前后崩溃、压实后旧偏移误读防护

## 运行

```sh
GO111MODULE=on go test ./...
```
