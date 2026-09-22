# 缓存依赖分析内核

从文件访问轨迹生成可靠缓存依赖的分析内核。纯 Python 标准库（3.11+），无第三方依赖。
不做系统调用拦截：输入是合成虚拟文件系统（VFS）产生的带序号操作轨迹。

## 模块

| 文件 | 职责 |
|---|---|
| `trace_kernel/trace.py` | 轨迹记录格式与 `TraceLog`（JSON Lines 持久化） |
| `trace_kernel/vfs.py` | 合成虚拟文件系统：执行操作并发射轨迹 |
| `trace_kernel/evidence.py` | 轨迹段 -> 压缩依赖证据（可持久保存/恢复） |
| `trace_kernel/index.py` | 反向索引：依赖键 -> 任务集合，带访问日志 |
| `trace_kernel/kernel.py` | 分析内核：三态结论 + 最短解释链 |
| `trace_kernel/replayer.py` | 独立 VFS 重放器（判定正确性的客观判据） |
| `trace_kernel/__main__.py` | CLI：`demo` / `query` |

## 核心语义

- **路径不是文件身份**：成功 `open` 绑定稳定对象 ID 与内容版本；改名/删除路径后
  已打开句柄仍指向原对象（`read` 按句柄记录对象与版本）。
- **硬链接共享对象**；**符号链接解析本身构成依赖**（目标改变即保守失效，即使最终内容相同）。
- **失败查询**记录实际解析过的父目录链与缺失名（resolution steps），并区分
  `perm_denied` 与 `not_found`。
- **目录枚举**区分 `full`（记录成员名集合）与 `prefix`（保守依赖整个成员集合）。
- **时间语义**：每条依赖记录观察发生的 `seq`；只有序号更大的事件才可能使其失效，
  任务观察之前的事件已包含在观察结果中。
- **不一致快照**：同一对象被观察到多个内容版本时，证据标记 `inconsistent`，
  永远不能批准缓存。

## 轨迹格式

每条记录是可 JSON 序列化的 dict：`{seq, task, op, ...}`。
任务侧操作 `open/read/lookup/enum`；环境变更操作
`write/create/mkdir/rename/link/symlink/set_target/unlink/chmod/external`。
解析链 step 形如 `{"dir":oid,"name":n,"obj":oid}`（成功）、
`{"symlink":oid,"target":t}`（链接跳转）、
`{"dir":oid,"name":n,"error":kind}`（失败分量，必为最后一步）。
完整字段定义见 `trace_kernel/trace.py` 模块 docstring。

## 依赖证据与反向索引

证据按任务压缩为六类依赖：路径解析链、负查询（缺失名/权限）、内容读
（按对象合并版本）、目录枚举（full/prefix）、符号链接目标、读权限观察。
反向索引键：`("member",dir,name)`、`("content",obj)`、`("enum",dir)`、
`("symlink",obj)`、`("perm",obj)`。无关子树的变更只查受影响的索引项、
只重算候选任务；`replace_task`/`remove_task` 整体增删，不残留旧依赖。

## 查询库

```python
from trace_kernel import AnalysisKernel, VFS
from trace_kernel.replayer import run_task_program

vfs = VFS(); vfs.populate({"a": {"f": "hello"}})
vfs.current_task = "T1"
run_task_program(vfs, [("open", "/a/f", "h"), ("read", "h")])
vfs.current_task = None
vfs.write("/a/f", "changed")

kernel = AnalysisKernel()
kernel.ingest(vfs.trace.records)
kernel.evaluate()
print(kernel.explain("T1"))     # [INVALID] + 最短解释链
kernel.save("evidence.json")    # 证据持久化
```

三态结论：`valid`（缓存仍有效）/ `invalid`（必须失效，附解释链）/
`insufficient`（证据不足，附具体原因——唯一来源是未建模的 `external` 事件，
不允许用“全部失效”代替）。

证据恢复：

```python
kernel2 = AnalysisKernel.restore("evidence.json", events=kernel.events)
kernel2.evaluate()              # 结论与恢复前一致
```

CLI：

```bash
python3 -m trace_kernel demo --out demo_out          # 生成 trace.jsonl/evidence.json/events.jsonl
python3 -m trace_kernel query demo_out/evidence.json demo_out/events.jsonl
```

## 判据与测试

`replayer.oracle_valid` 是独立判据：在变更后的终态文件系统上重放任务程序，
比较任务可观察结果（成功/错误类别、内容、枚举成员；不含内部对象 ID）。
内核判定不得比判据更乐观。

测试（`python3 -m unittest discover -s tests`，21 个用例全部通过）：

- `test_vfs.py`：句柄在改名/删除后仍指原对象、硬链接共享、权限错误与不存在分离、
  符号链接解析入轨迹、枚举 full/prefix 区分。
- `test_kernel.py`：五个重点场景——失败读后创建文件、打开后同路径换成另一对象、
  链接指向改变但最终内容相同（保守失效+具体链）、删掉一个硬链接（经另一路径的任务不受影响）、
  目录前缀消费后新增成员；另有不一致快照不得批准、外部变更的证据不足具体原因。
- `test_index.py`：5 个互不相关子树中修改一处，只访问 `("content", 受影响对象)` 一个索引项、
  只重算受影响任务；任务重算/删除后索引无残留。
- `test_persistence.py`：证据保存/恢复后结论与解释链逐字一致。
- `test_exhaustive.py`：穷举小对象集（3 目录 / 文件 / 硬链接 / 符号链接 / 不可读目录）
  上 8 个任务程序 × 全部合法变更序列（长度 ≤ 2，含读写交错）共 **1072** 用例对拍：

  ```
  {'valid': 529, 'invalid': 543, 'insufficient': 0, 'conservative': 77, 'cases': 1072}
  ```

  - 内核判 `valid` 的 529 例全部与判据一致（零错误批准）；
  - 77 例保守失效（符号链接改指同内容、前缀枚举、打开后未读即写等）每例都带具体解释链；
  - 批准率约 49%，证明未用“全部失效”满足正确性；
  - 读写交错测试确认：写入夹在两次读之间时结论为 `invalid`（快照不一致），
    写入先于全部观察时结论为 `valid`。
