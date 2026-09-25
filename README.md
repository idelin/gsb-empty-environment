# proofpack

从指定结论（根目标）反向裁剪完整依赖子图，导出可独立离线重放的证明包。
纯 Python 3.11 标准库实现，测试使用 `unittest`，样例数据为合成数据。

## 功能

- **反向裁剪**：从根目标出发沿规则反向链收集依赖，只导出证明所需的事实、
  规则与推导步骤；无关证据不进入包。
- **替代证明与精确优化**：同一结论可有多条证明路径。目标按字典序优化：
  先最少泄露可公开事实，再最小包大小（事实 + 规则 + 步骤）。共享事实/规则
  通过集合并集只计一次。搜索在有限图上精确进行（按集合包含剪枝被支配的
  选项，环分支被切断——最小证明必为无环图）。
- **未决界限**：枚举组合数超出预算时返回 `undecided` 结果，携带分量级
  下界（各目标独立最优的最大值）与上界（各目标最优选项的并，仍为完整
  有效证明，可正常导出）。
- **稳定标识与字节**：事实、规则、步骤均带稳定 ID 与版本（步骤 ID 为
  `step:<rule-id>`）。导出为规范化 JSON（键排序、无空白、UTF-8），相同
  输入产生稳定字节，与知识库插入顺序无关。摘要含计数、公开事实清单与
  对包体的 sha256。
- **离线验证器**：不信任包内任何"已证明"标记——重新校验摘要、检查无悬空
  引用、步骤与规则一致，并从包内基础事实前向链接重推全部根目标；非基础
  事实必须有推导步骤，包内不得含多余项。`check_necessity` 逐项删除并重签
  摘要后要求验证失败，确认每项皆必要。

## 布局

- `proofpack/model.py` — 事实/规则/知识库模型与校验、环检测
- `proofpack/search.py` — 精确证明子图搜索、成本模型、未决界限
- `proofpack/package.py` — 导出包构造、规范化字节、校验摘要
- `proofpack/verify.py` — 离线验证器与必要性检查
- `proofpack/sample.py` — 合成样例知识库（补助资格域）
- `run_sample.py` — 运行样例
- `tests/test_proofpack.py` — 23 个单元测试

## 运行

```sh
python3 run_sample.py                    # 裁剪、导出、验证、必要性检查
python3 -m unittest discover -s tests -v # 测试
```

## 样例输出（节选）

```
Root goals: ['f-eligible', 'f-means-tested']
Search status : exact
Cost          : public_facts=1, size=13
Bounds        : lower=(1, 13), upper=(1, 13)
Offline verification: ok=True errors=[]
Necessity (remove-any-item fails): ok=True violations=[]
Deterministic re-export identical: True
```

样例中 `f-means-tested` 有两条替代证明：私有路径（`f-income` +
`f-employment`，不新增公开事实）与公开路径（`f-income-redacted`，更小但
泄露一个公开事实）。优化器按"先最少公开事实"选择私有路径；两个根目标
共享的子证明只计一次（7 事实 + 3 规则 + 3 步骤 = 13）。

## 测试结果

```
Ran 23 tests in 0.018s
OK
```

覆盖：精确搜索与界限、公开事实优先于包大小、共享只计一次、无关证据排除、
稳定字节（含插入顺序无关）、摘要篡改检测、悬空引用检测、不信"已证明"
标记、删除任一必需前提失败、步骤-规则一致性、环安全、不可证目标报错。
