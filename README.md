# pubtool — 多文件原子发布工具

读者只能看到**完整旧版**或**完整新版**，永远不会读到混合版本。

## 目录设计

```
<root>/
  current             # 指针文件：当前生效版本 id（tmp 文件 + rename 原子切换）
  staging/<id>/       # 正在写入的新版本（对读者不可见）
  versions/<id>/      # 已提交版本目录，不可变（manifest.json + 数据文件）
  quarantine/         # 恢复时移入的未提交/损坏目录（只移动，不删除）
```

## 发布协议（四步，两个原子提交点）

1. 把数据文件写入 `staging/<id>`，同时计算每个文件的 SHA-256 写入 `manifest.json`；
2. **先校验**：重算校验和、核对清单、拒绝多余文件，校验不过绝不发布；
3. `rename staging/<id> -> versions/<id>` —— 目录级原子提交；
4. 写 `current.tmp` 后 `rename -> current` —— 指针原子切换，读者从此看到新版。

每一步前都有故障注入钩子（`Hook` / `Op`），测试用 `ErrCrash` 模拟断电重启。

## 读者一致性

`Acquire()` 在某一时刻钉住版本 id，之后所有读取都直接落在该版本
**不可变目录**上。已提交版本目录永不修改、永不删除，因此发布前获取
的旧读句柄（包括已 `Open` 的文件句柄）在新版发布后继续可用。

## 崩溃恢复（`Open` 时自动执行）

- `staging/` 残留 = 未提交目录 → 移入 `quarantine/`，不删除；
- 指针丢失 / 内容非法 / 指向缺失或损坏版本 = 坏指针 → 扫描
  `versions/` 回退到最新有效版本并重建指针；
- 校验失败的版本目录 → 移入 `quarantine/` 保留现场；
- **不认识的目录/文件一律原样保留**，只在 `RecoveryReport` 中报告。

## 故障注入测试

`TestFaultInjectionMatrix` 在协议每一步（写数据、写清单、校验、
目录 rename、写指针、指针 rename）分别注入普通错误和 `ErrCrash`
（崩溃后重新 `Open` 走恢复），断言：

- 当前版本始终是完整的旧版；
- 故障前获取的快照仍可完整读取；
- 清除故障后重试发布成功，读者切换到完整新版。

`TestConcurrentPublishAndReadNeverMixed` 用 1 个写者 + 4 个读者并发
验证任何快照内所有文件的版本标签一致（`-race` 通过）。

## 使用

```sh
go test ./...                      # 运行全部测试（含故障注入矩阵）
go build ./cmd/pubtool
pubtool -root data publish ./dir   # 发布一个目录
pubtool -root data current         # 查看当前版本
pubtool -root data list            # 列出当前版本文件
pubtool -root data cat <file>      # 读取文件
pubtool -root data report          # 查看恢复报告
```
