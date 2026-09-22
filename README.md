# cachekernel — 文件访问轨迹的缓存依赖分析内核

纯 Python 3.11 标准库实现。不做系统调用拦截：输入是合成虚拟文件系统（VFS）
和带序号的操作轨迹，输出是可持久化的依赖证据、反向索引，以及面对下一次
文件系统变更时的三值缓存判定（有效 / 必须失效 / 证据不足）与最短解释链。

## 模块

- `cachekernel/vfs.py` — 虚拟文件系统：稳定对象 ID + 内容版本；路径解析链；
  打开/读/枚举目录/改名/硬链接/符号链接/删除/权限变更；每个操作产生一条
  带序号的轨迹事件，变更同时记录效果（`fx`）。
- `cachekernel/evidence.py` — 把任务轨迹压缩成依赖证据（去重、排序、JSON 可持久化）。
- `cachekernel/index.py` — 反向索引：依赖键 → 任务集合；带访问日志。
- `cachekernel/query.py` — 变更捕获（富事实增量）与三值判定 + 最短解释链。
- `cachekernel/oracle.py` — 独立 VFS 重放器，作为测试判据。

## 核心语义

- **路径不是身份**：成功的 `open` 绑定稳定对象 ID 与内容版本；改名/删除
  路径后，已打开句柄仍指向原对象（对象不做 GC）。硬链接共享对象。
- **符号链接解析本身是依赖**：解析链中的每个 symlink 跳点都记录
  （对象 ID, 版本）。
- **失败查询记录实际解析位置**：`miss` 终端记录（父目录 ID, 目录版本,
  缺失名），而不是最终路径字符串。`EACCES`（权限）与 `ENOENT`（不存在）
  分开表示；权限翻转会 bump 对象版本，因此可被版本依赖捕获。
- **目录枚举区分完整/前缀**：前缀消费保守依赖整个目录成员集合；成员变化
  时判定为「证据不足」并给出具体原因，而不是臆断结果已变。
- **read-your-own-writes**：任务自身改动过的键，其后续观察被重写为该键的
  外部基线版本（首次自身改动前观察到的版本）；任务新建对象的观察是内部
  的，被丢弃（由创建时的 miss 依赖兜底）。
- **不一致快照不批准**：同一对象在任务内被观察到多个外部版本
  （`non_atomic`），变更触及该对象时绝不判「有效」。

## 轨迹格式

每个事件是一个 dict（JSON 可序列化），带全局序号 `seq`：

```json
{"seq": 7, "task": "t1", "op": "open", "path": "/d/f",
 "chain": [{"t": "walk", "dir": 0, "dirv": 4, "name": "d", "obj": 1}],
 "result": {"ok": {"obj": 4, "ver": 2, "handle": 1, "kind": "file"}},
 "fx": [["obj", 4]]}
```

- `chain`：解析实际经过的步骤。`walk`（目录项绑定）与 `sym`（符号链接跳点）。
- `result`：`ok` 或 `error`。失败时带 `miss` 终端
  `{"dir": d, "dirv": v, "name": n}`；权限错误为 `EACCES`，不存在为 `ENOENT`。
- `result.mem`：变更类操作记录的成员资格前提（改名/链接/删除前目录项绑定）。
- `result.ok.pre_ver`：`write`/`chmod` 记录的变更前对象版本（外部观察）。
- `fx`：该变更操作触碰的 `[["dir"|"obj", id], ...]` 效果。
- `enumdir` 的 `result.ok.mode` 为 `"full"` 或 `"prefix"`。

## 依赖证据

`build_evidence(task_id, events)` 把轨迹压缩为：

```json
{"task": "t1",
 "deps": [{"dep": ["walk", 0, 4, "d", 1], "src": {"seq": 7, "op": "open", "path": "/d/f"}}],
 "non_atomic": [],
 "ops": 3}
```

依赖种类：`walk`（目录项绑定）、`miss`（缺失名）、`sym`（符号链接版本）、
`obj`（对象内容/权限版本）、`enum`（目录枚举，full|prefix）。
`evidence_to_json` / `evidence_from_json` 提供持久化与恢复；恢复后的证据
与原始证据判定结果完全一致（测试覆盖）。

## 查询库

```python
change = apply_change(snap, [("write", "/d/nope", b"N")])  # 捕获富事实增量
cand = index.candidates(change.affected)                   # 只访问受影响索引项
verdict, expl = verdict_for(evidence, change)              # 三值判定 + 解释链
```

- `Change` 是自包含、可持久化的增量：每个被触碰的目录/对象记录完整结果
  事实（`to_json` / `from_json`）。判定只依赖证据 + 增量，无需在线文件系统。
- 判定语义：变更应用于任务前状态；缓存有效 ⟺ 在变更后的状态中重跑任务
  会复现全部已记录输出（由 oracle 直接实现该定义）。
- `VALID`：所有外部依赖在增量下仍然成立。
- `INVALID`：某条具体依赖被违反，输出最短失败链
  （`seq N op('path'): 依赖 -- 违反原因`）。
- `INSUFFICIENT`：保守依赖被触发（前缀枚举的目录成员变化、非原子快照
  被触及），输出具体原因；绝不用「全部失效」满足正确性。

## 反向索引

`ReverseIndex` 把每个依赖映射为 `("dir", id)` / `("obj", id)` 键。
`candidates(affected)` 只查找变更实际触及的键并记录访问日志；
`remove(task)` 删除任务的全部依赖，不留残余（`is_empty()` 可验证）。
固定互不相关子树的测试中，修改一处只访问受影响的索引项。

## 测试

```
python3 -m unittest discover -s tests -t .
```

- `tests/test_kernel.py`（11 项）：失败读后创建、打开后同路径换对象、
  链接指向改变但最终内容相同、删除一个硬链接、前缀消费后新增成员、
  EACCES/ENOENT 区分、非原子快照、改名后句柄存活、索引局部性、
  索引删除无残余、证据 JSON 往返。
- `tests/test_exhaustive.py`：在小对象集（目录/文件/符号链接）上穷举
  长度 ≤ 3 的合法读写交错（15 个操作模板 ⇒ 3,615 条轨迹）× 9 类变更
  = 32,535 个（轨迹, 变更）对，逐对用独立重放器交叉验证：

  - 内核判 `valid` ⇒ oracle 必须能复现全部记录输出（零违规）；
  - 每个保守判定都带具体原因；
  - 三种判定均实际出现（非平凡保守、非平凡批准）。

  最近一次运行：`valid=14738, invalid=16610, insufficient=1187`。
