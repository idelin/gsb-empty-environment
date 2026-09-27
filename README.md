# symmc：进程 ID 置换对称压缩模型检查器

面向**声明同构进程**的有限状态模型检查器（Python 3.11，仅标准库，测试用 unittest）。
对声明对称的进程组做状态置换规范化，每个等价类只保留一个代表；反例保留
ID 映射，可还原、可在原始模型上逐步重放；搜索未完成时结论保持未决。

## 设计

- **模型**（`symmc/model.py`）：全局状态为 `(shared, locals_)`。迁移由角色
  `Role` 定义，只依赖 `(local, shared)`，与进程 ID 无关，因此同角色进程天然
  同构。声明对称组时校验：不越界、不重叠、不混同不同角色（否则抛
  `SymmetryError`）——有特殊角色的进程（如领导者）不会被随意置换。
- **规范化**（`Model.canonicalize`）：对称组内按局部状态排序（同值按原 pid
  升序，确定性），组外位置不动。返回 `(规范代表, mapping)`，
  `Model.restore(canonical, mapping)` 可还原原状态——两同构进程交换状态后
  只对应一个代表。
- **对称条件验证**（`Model.audit_symmetry`）：在具体状态空间上有界枚举，
  检查组内任意对换与迁移关系可交换；超出状态上限返回 `None`（未决）。
- **检查器**（`symmc/checker.py`）：BFS 不变式检查，三值结论
  `SAFE / VIOLATED / PENDING`。`max_states` / `max_depth` 截断即未决。
  每个规范代表保存一个具体代表状态，反例路径完全由具体状态构成，
  每步附带 `(canonical, mapping)`，`Result.replay(model)` 在原始（未压缩）
  模型上逐步重放并确认末态违反不变式。

## 目录

- `symmc/model.py` — 模型、置换规范化、ID 映射还原、对称条件审计
- `symmc/checker.py` — 对称压缩 BFS、三值结论、可重放反例
- `symmc/examples.py` — 互斥协议（含缺陷版本）、领导者模型
- `demo.py` — 运行样例
- `tests/test_symmc.py` — 20 个 unittest 用例

## 运行

```sh
python3.11 demo.py                              # 运行样例
python3.11 -m unittest discover -s tests -v     # 运行测试
```

## 运行样例（实测输出摘要）

```text
1. mutex-3   压缩前 safe/20 状态     压缩后 safe/7 状态      性质一致 True
2. mutex-3-buggy 压缩前后均 violated；反例 5 步，每步含 mapping，
   末态违反不变式 True，原始模型上重放通过 True
3. leader-2  对称组仅 ((1, 2),)；(0,(2,1,0)) 规范化为 (0,(2,0,1))，
   领导者位置不动；与 leader 交换后的状态代表不同（未混同）
4. audit_symmetry: mutex-3 / leader-2 均 True
5. mutex-4, max_states=3: verdict=pending（未搜索完保持未决）
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`：**Ran 20 tests — OK**，覆盖：

- 同构进程交换状态只一个代表、ID 映射还原、规范化幂等、压缩比；
- 领导者不可置换、混合角色对称组被拒绝、非法对称组（越界/重叠/过小）被拒绝；
- 对称条件审计通过与超限未决；
- 压缩前后安全/缺陷小模型结论一致；
- 反例可重放、每步 ID 映射可还原、篡改后重放失败；
- 状态/深度上限截断为 PENDING，上限内发现违反仍报 VIOLATED。
