# graphdiff

纯 Python 3.11 标准库实现的版本化图比较 / 可逆补丁 / 三方重放库（测试用 `unittest`）。

## 模型

- 顶点与边都有**字符串 ID**；边的身份是边 ID，而不是端点对。
- 支持**孤立顶点**与**多重边**（同一端点对上的多条边以不同 ID 共存）。
- 顶点/边携带属性字典与**左闭右开**的有效区间 `[start, end)`（`Interval.contains(t)` 即 `start <= t < end`）。
- 属性比较采用 JSON 语义（`1` 与 `true` 不相等），与内容指纹保持一致。

## 功能

### 1. 两版本比较 `diff(base, target, rename_map)`

- 显式重命名映射 `rename_map={旧名: 新名}`；`check_rename_map` 检测映射冲突并抛出
  `RenameMapError`，携带结构化 `RenameConflict`：
  - `missing_source`：源节点不存在；
  - `duplicate_target`：多个源映射到同一目标；
  - `target_occupied`：目标名被未参与重命名的节点占用。
  - 链式与交换式重命名允许，内部展开为无碰撞顺序（环用临时名旋转）。
- 变更分类：**新增/删除**（按 ID）、**属性/区间变化**（`SetAttr`/`SetInterval`，记录旧值作为来源）、
  **边重连**（`ReconnectEdge`）。
- 重命名不删除重建边：`A 重命名 X 且边不变`时，补丁只有 `RenameNode`，端点随改名迁移，
  不会出现全部边的删除+新增。

### 2. 可逆补丁 `Patch`

- 每个 op 自带旧值/新值，`patch.inverse()` 无需原图即可求逆。
- 前置版本校验两段式：先比摘要（`sha256`，可注入替换），**再比对规范内容**——
  摘要碰撞（哪怕是伪造的）也无法让错误基线通过（`ContentMismatchError`）。
- `apply` 返回新图、从不变更入参；错误基线抛出 `BaseMismatchError` 且原图保持不变。
- 补丁可序列化为 JSON（`to_json`/`from_json`），含 base/target 摘要与内容指纹。

### 3. 组合 `compose(p1, p2)`

- 要求 p1 的目标版本与 p2 的基线版本**摘要与内容完全一致**，否则 `ComposeError`。
- 合并后按交换律做不动点化简（重命名链、属性写合并、加删抵消、重连折叠、
  删后再加转为修改），保留节点身份（字符串 ID）、边重数与属性来源（最旧的旧值）。
- 随机化性质测试验证：`apply(compose(p1,p2), A) == C`、`inverse` 恢复 `A`、结合律。

### 4. 三方重放 `replay(patch, other, other_renames)`

- 把补丁的意图重放到已有独立修改的基线 `other` 上；对方分支的重命名同样由显式映射给出。
- 不冲突修改自动**重定位**：节点引用沿对方重命名翻译（含 `DelEdge` 存储的端点），
  并记录 `Relocation`。
- 结构化冲突 `Conflict(kind, entity, entity_id, ours, theirs)`：
  `rename_vs_delete`、`rename_vs_rename`、`same_name_different_identity`（点/边）、
  `delete_vs_modify`、`modify_vs_delete`、`attr_vs_attr`、`reconnect_vs_delete` 等。
  有任何冲突则不应用任何修改。

## 运行

```bash
python3.11 examples/demo.py     # 运行样例
python3.11 -m unittest discover -s tests -v
```

## 运行样例（节选）

```
=== 1. diff(base, target) with explicit rename map {'api': 'gateway'}
   RenameNode('api' -> 'gateway')
   DelEdge('e2')
   AddNode('worker', attrs={'lang': 'go'}, iv=Interval(start=None, end=None))
   AddEdge('e3': 'gateway'->'worker')
   SetAttr(node 'db'.'lang': 'sql' -> 'rust')
note: no DelEdge/AddEdge for e1 -- the rename does not re-create edges

=== 4. wrong baseline is rejected and left unchanged
  rejected: base digest mismatch: patch expects 7df643db19dc..., graph has cc4780385c96...

=== 5. forged digest collision is caught by content re-check
  rejected despite matching digest: base digest matched but content differs; ...
  true baseline still accepted: True

=== 7. three-way replay onto a moved baseline
  ok: True
  relocated: DelEdge - edge 'e2' stored endpoints relocated 'gateway','db' -> 'gateway','store'
  relocated: SetAttr - node 'db' relocated to 'store'

=== 8. structured conflicts
  conflict: kind=rename_vs_delete entity=node 'api'
  conflict: kind=same_name_different_identity entity=node 'worker'

=== 9. half-open intervals [start, end)
  at t=10: ['v'] (left closed)
  at t=20: [] (right open)
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
Ran 49 tests in ~1.6s
OK
```

- `tests/test_diff.py`：重命名映射冲突、变更分类、孤立点/多重边、半开区间。
- `tests/test_patch.py`：应用/求逆/组合、错误基线拒绝且原图不变、摘要碰撞内容复核、JSON 往返。
- `tests/test_merge.py`：重定位与各类结构化冲突、冲突时不改动对方基线。
- `tests/test_randomized.py`：随机图上的组合等价、逆向恢复、结合律、随机错误基线/碰撞基线拒绝、
  随机重放重定位（另用 1500+ 组随机试验做过压力验证）。

## 目录

```
graphdiff/
  model.py    # Graph/Vertex/Edge/Interval，JSON 语义相等性，规范序列化
  ops.py      # 原子操作（自带前置检查与求逆）
  diff.py     # 两版本比较 + 重命名映射校验 + 重命名序列展开
  patch.py    # Patch、两段式基线校验、inverse、compose/simplify
  merge.py    # 三方重放、重定位、结构化冲突
  hashing.py  # 可注入的摘要函数（测试用弱摘要制造碰撞）
examples/demo.py
tests/
```
