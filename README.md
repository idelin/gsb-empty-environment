# 离线镜像存储库 (offline image repository)

Python 3.11 标准库实现，无第三方依赖，不访问远程包仓库。

## 运行

```bash
python3 run_tests.py          # unittest 全套
python3 replay_demo.py DIR    # 在全新目录重放需求场景
```

## 目录布局

```
objects/<xx>/<sha256>   SHA-256 寻址、声明长度核验；同摘要内容硬链接共享
meta/<name==ver@plat>.json
snapshots/<sid>.json    自包含清单（roots/runtime/build/metadata/objects + manifest_digest）
current.json            当前快照指针（临时文件 + 原子替换）
leases/<lease>.json     显式读租约（不靠墙钟过期）
stage/ tmp/             暂存目录，崩溃恢复后清空
journal.log             校验和追加日志（恢复的唯一事实源）
```

## 包身份与元数据

身份为 `(name, version, platform)` 三元组（`Ident`）。目标包平台例如
`linux-x86_64`；宿主构建工具恒为 `host`。元数据声明：

* `runtime_deps`：运行依赖，停留在所属平台；
* `build_deps`：宿主构建工具依赖，依赖边跳到 `host` 平台。

快照只有在**两类闭包都齐全**时才可发布：运行时闭包（目标平台上的传递
`runtime_deps`）与宿主闭包（所有 `build_deps` 边到 `host`，宿主包的两类
依赖都留在 host，并包含运行时闭包本身）。

导入时必须提供或核验摘要与长度：声明不符直接拒绝；归档落盘后再次计算
SHA-256 与长度核验。缺件分三类并附“根 → … → 缺件”的路径：

* `metadata`：身份无元数据；
* `content`：元数据存在但归档缺失；
* `digest`：归档存在但摘要/长度不符。

## 线性化点 (linearization points)

所有结构变更由一把 `RLock` 串行化；每个操作的持久化线性化点是其决策记录
落日志（fsync）或一次原子替换：

| 操作 | 线性化点 |
|---|---|
| 导入开始 | `IMPORT_BEGIN` 追加（此后其对象是 GC 根，直到 COMMIT/ABORT） |
| 导入可见 | `IMPORT_COMMIT` 追加（先日志，后幂等补写元数据文件） |
| 导入中止 | `IMPORT_ABORT` 追加 |
| 快照开始 | `SNAP_BEGIN` 追加（携带完整对象集合；GC 从扫描起固定它） |
| 快照发布 | `current.json` 的原子替换（崩溃后见指针即已发布，恢复补 COMMIT） |
| 租约授予 | `LEASE_GRANT` 追加，随后租约文件落盘（恢复重放） |
| 租约终止 | `LEASE_RELEASE` 追加（显式释放，或 reap_leases(holder) 恢复协议） |
| GC 扫描 | `GC_BEGIN` 追加（持锁枚举全部对象/清单/活动意图/租约/当前快照） |
| GC 删除 | 每个 GC_UNLINK 记录在 unlink 之前（删除可安全重做） |
| GC 结束 | `GC_DONE` 追加并唤醒等待中的发布者 |

## 代际标记-清扫与“扫描后新发布”

`GC_BEGIN` 在一个临界区内完成枚举并把候选集与**当时**的所有根写入日志。
标记集合取“扫描栅栏根 ∪ 标记时刻新根”，因此扫描后释放的租约本轮仍受保护；
扫描后开始/在途的导入在每次删除前再栅栏（re-fence）一次，其对象不会误删。

快照的 `SNAP_BEGIN` 若发生在某代 GC 的窗口内（fence 后、DONE 前），会阻塞
到该代结束：本轮标记后的新发布绝不可能引用本轮要扫掉的对象。若发布者在栅
栏之后才声明一组全新根，其引用的对象若已被当作垃圾，则正确得到 `content`
缺件，而不会被“复活”。意图在栅栏**之前**建立的快照，其对象集合从扫描起即
被固定，提交可以与扫描/标记并发并安全存活（见并发测试）。

只有当前快照、有效租约快照和在途提交/导入需要的对象被保留。租约释放后下
一代 GC 才回收旧快照及其私有对象；被新旧版本共享的对象（如 libc）保留。

## 租约协议

租约无任何墙钟超时。终止只来自：

1. `release_lease`；
2. 带外确认持有者进程死亡后，显式调用 `reap_leases(holder)`（恢复协议）。

持有有效租约的读者通过 `read_archive` 读取，读取时重新计算 SHA-256/长度。

## 崩溃恢复（仅进程崩溃）

日志帧：16 字节魔数 + 重复 `[crc32,type,len|JSON]`。打开仓库时：

1. **半写尾记录可截断**：合法前缀之后的任何残余（零填充、半个帧、坏帧且
   之后没有合法链）被丢弃并截断；
2. **中间损坏拒绝恢复**：损坏帧之后若还能在文件后部找到一条对齐到 EOF 的
   完整合法帧链（崩溃不可能在半写之后再产生记录），抛 JournalCorrupt；
3. 重放重建状态：
   * 已提交快照（有 SNAP_COMMIT，或 current.json 已原子指向但 COMMIT
     丢失）保持可见并补写提交；
   * 孤立的在途 SNAP_BEGIN/清单、在途导入及暂存元数据判为孤立事务，
     追加 ABORT、删除清单；其已发布的内容寻址对象作为普通垃圾留给 GC；
   * 租约以日志为准（GRANT 无文件则补文件，RELEASE 有文件则删文件）；
   * 崩溃在 GC 中途的代，依据 GC_BEGIN/GC_MARK 续完（删除幂等）；
   * stage/、tmp/ 整个清空（存活对象永不存放在那里）。

## 测试

* `tests/test_closure.py`：独立集合闭包、三类缺件与根路径、宿主平台规则；
* `tests/test_journal.py`：追加、半写尾截断、中间损坏拒绝、坏魔数；
* `tests/test_scenario.py`：需求主场景、声明校验、内容共享、不可变性、
  在途导入为 GC 根、租约非墙钟、独立不可变审计；
* `tests/test_concurrency.py`：注入事件驱动的发布/释放/回收交错与阻塞；
* `tests/test_crash.py`：导入、清单提交、租约、删除边界逐点注入崩溃后
  重开并审计；
* `imagerepo/checker.py`：独立审计器（不依赖仓库内存状态），校验每个可见
  快照两类闭包完整且清单摘要未被修改。
