# pathindex

维护“对象 ID → 多级路径”索引的 Go 库（仅标准库，兼容 Go 1.11+）。

- ID 终身不变，重命名/移动只改父指针与名字，不碰 ID。
- 子树移动是原子的：整批校验通过后一次性落盘，版本号 +1。
- 路径缓存按版本失效：缓存条目只在 `cacheVersion == version` 时有效，
  任何已提交的变更都会让旧路径立即不可见。
- 所有文件都放在调用方 `Open(dir)` 指定的目录里（`state.json` +
  提交期的临时文件 `state.json.tmp`）。

## 使用

```go
s, _ := pathindex.Open("/path/to/dir")
s.Create("PROJ", "", "proj")        // 根 ID 为 ""
s.Create("SRC", "PROJ", "src")
s.Move("SRC", "OTHER")              // 原子移动子树
s.Rename("PROJ", "project")         // ID 不变
s.MoveBatch([]pathindex.MoveOp{...}) // 整批原子提交
p, _ := s.Path("SRC")               // 版本键控缓存
```

## 校验规则

- **循环**：对批次**终态**做祖先链检查。把 A 移到它自己的子目录 B
  （`Move("A","B")`，B 是 A 的后代）会被拒绝；多个操作组合出的环
  （A→B 且 B→A）同样被拒绝。
- **重名冲突**：对批次**终态**构建 `(parent, name) → id` 全量映射，
  与操作顺序无关——两个操作最终落在同一槽位必然报错；一个操作占用的
  槽位被同批次另一个操作让出则合法（交换名字可通过）。
- **失败回滚**：校验或落盘任一环节失败，内存与磁盘状态都保持原样，
  版本号不变，所有路径不变。

## 崩溃恢复

提交流程：写 `state.json.tmp` → `fsync(文件)` → `rename` 为
`state.json` → `fsync(目录)`。`Open` 即恢复点：

- 残留 `state.json.tmp` 说明崩溃发生在 rename 之前，直接删除，
  `state.json` 仍是上一次完整提交的版本。
- `state.json` 永远是“旧版本”或“新版本”之一，绝不出现撕裂状态
  （rename 在 POSIX 上是原子的）。

## 断电保证的边界

- **保证（原子性）**：在遵守 fsync 语义的 POSIX 文件系统上，任何时刻
  恢复出的都是某个完整提交的版本；提交返回成功（目录 fsync 完成）后，
  该版本 durable。
- **不保证**：
  - rename 已发生、目录 fsync 尚未返回时断电，恢复结果可能是新版本
    （对本库这仍算正确：调用方没收到成功应答，新旧皆可）。
  - 文件系统/硬件虚报 fsync（带失控写缓存的磁盘、某些 USB 设备、
    非日志文件系统的极端情况）时，durability 依赖底层，库无法兜底。
  - 多进程/多 goroutine 并发写同一目录：库假设单写者，未做跨进程锁。
  - 状态文件被外部篡改/截断：`Open` 报 corrupt 错误，不做自动修复。

## 故障注入

`Store.FailAt` 是注入点：`FailBeforeCommit` / `FailAfterTmpWrite` /
`FailAfterRename`，触发后操作返回 `ErrInjected`，用于测试与演示崩溃
恢复路径（见 `store_test.go` 与 `cmd/demo`）。

## 运行

```sh
go test ./... -v
go run ./cmd/demo -dir /tmp/pathindex-demo
```

测试结果：13 个用例全部 PASS（创建/路径、子树移动、A→自身子目录拒绝、
跨操作成环拒绝、批次顺序无关的重名检测、槽位让出合法、失败批次回滚、
三个注入点恢复、残留临时文件清理、重命名 ID 稳定+缓存失效、重开持久化、
重复创建拒绝）。演示输出见 `cmd/demo/main.go` 各阶段打印。
