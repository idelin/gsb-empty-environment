# 离线镜像存储库（Python 3.11 标准库）

从空目录开始、内容寻址（SHA-256）的包/快照存储库。无任何第三方依赖，
也不访问远程包仓库。崩溃通过注入事件模拟，恢复仅针对进程崩溃。

## 包身份与对象

* 身份 = `(name, version, platform)`，平台区分目标包与宿主构建工具。
* 元数据声明 `run_requires`（目标运行依赖）与 `host_requires`
  （宿主构建工具依赖）；元数据先规范化再按自身 SHA-256 存为对象。
* 归档流式导入，边读边核对**声明长度**与**声明摘要**；不一致抛
  `LengthMismatch`/`DigestMismatch`，暂存文件立即清除。
* 相同摘要天然共享：对象落在 `objects/xx/<62 位>`，提交用临时文件 +
  `os.replace`，读者只能看到完整对象。
* 未完成的导入不会进入 catalog，任何快照清单都引用不到它。

## 两类依赖闭包

发布根包时独立计算并要求两类闭包都齐全：

1. 运行闭包：根 + 全部 `run_requires` 传递可达。
2. 宿主构建闭包：运行闭包中每个包声明的 `host_requires`，再按两种边闭包
   （宿主工具自身也有运行依赖、也可能需要构建工具）。

缺件报告分三类并附带 **根 → 缺件** 路径：

* `metadata-missing`：坐标未导入；
* `content-missing`：元数据在但归档对象缺失；
* `digest-mismatch`：对象在但重新哈希不匹配。

闭包计算器在 `imagestore/closure.py`（纯 BFS 最小不动点），
`imagestore/audit.py` 另有一份**独立重写**的遍历/校验器直接读磁盘。

## 快照发布的线性化点

发布顺序：闭包校验（持锁）→ 临时清单 fsync → 追加
`snapshot-publish` 日志 → `os.replace`（**发布线性化点**）→ 可选
追加 `current`。因此：

* 替换前崩溃：只有孤立临时文件，恢复时删除；
* 日志已提交但替换前崩溃：恢复依据日志重建不可变清单；
* `current` 前崩溃：快照存在但不是当前。

## 租约与垃圾回收

* 租约**不按墙钟过期**，只能由 `release_lease` 或 `recover_lease`
  （崩溃后的显式恢复协议）终止；获取/释放/恢复都追加持久记录。
* 读者经租约访问，每次读取都在锁内重新确认租约仍有效。
* GC 为代际标记-清除。标记根：当前快照、所有有效租约固定的快照、
  正在提交的导入（`import-begin` 已持久化）声明的摘要。
* 标记后释放锁，注入点 `gc.after_mark_lock` / `gc.sweep_yield`
  允许并发发布/释放交错；每个对象在 `unlink` 前都在锁内**重新计算活集**，
  所以“标记之后才发布/重新固定的快照”不会被本轮扫描误删。
* 删除逐对象落 `gc-delete` 日志（含遗忘的 catalog 坐标），`gc-end`
  完成一代。删除中途崩溃：重启丢弃旧计划，已删对象不会恢复，未删对象
  留给下一代；任意重启后可见快照仍可通过独立审计。

### 线性化点一览

| 操作 | 线性化点 |
| --- | --- |
| 导入 | 校验后追加 `import` 日志 |
| 发布快照 | `os.replace` 清单（提交日志已先落盘） |
| 设当前 | 追加 `current` 日志 |
| 获取租约 | 追加 `lease-acquire` |
| 释放租约 | 追加 `lease-release` |
| 恢复租约 | 追加 `lease-recover` |
| GC 标记 | 标记后重入锁；每次 unlink 前锁内重判活集 |
| GC 删除 | 单个对象的 `os.unlink` |

## 日志恢复

帧 = `IMJ1`(4) + 长度(8) + JSON 负载 + 负载 SHA-256(32)，追加后 fsync。

* 末尾不完整帧 = 半写尾记录：截断并 fsync；
* 完整帧摘要错、魔数错但头部完整、负载不可解码 = 中间损坏：抛
  `JournalCorrupt`，**拒绝恢复**。
* 重放区分已提交快照与孤立暂存/临时清单；重建 `catalog.json`、
  `current`、租约文件；中止未完成导入并清扫其独占对象。

## 目录

```
imagestore/
  errors.py    异常类型（缺件报告挂在 ClosureIncomplete.issues）
  events.py    注入事件总线 / InjectedCrash(BaseException)
  journal.py   帧格式、追加、恢复扫描（截断尾帧/拒绝中间损坏）
  content.py   SHA-256 对象存储、流式校验、同摘要共享
  closure.py   两类闭包的纯集合计算与缺件分类
  repo.py      导入/发布/租约/读取器/GC/崩溃恢复
  audit.py     独立磁盘审计器（自带 BFS 与哈希校验）
  fixture.py   确定性重放夹具与并发调度
tests/         unittest 套件
run_tests.py   标准库测试入口
```

## 运行

```
python3.11 run_tests.py
```
