# 合成数据血缘删除/重算只读试算器

这是一个仅使用 Python 3.11 标准库和 `unittest` 的最小实现。它根据“主体 → 原始记录 → 派生产物”的 DAG 血缘，生成删除或重算计划；当前版本只生成执行前的只读试算结果，不连接外部系统，也不修改输入目录。

## 范围与边界

- 只处理合成数据：`Subject.synthetic=True` 且 `Artifact.synthetic=True` 才允许入目录；未标记合成数据会直接失败。
- 原始记录、缓存和已发布统计分开处理，不把缓存失效误认为原始数据删除。
- 同一主体的多个别名必须显式列在 `Subject.aliases` 中，并映射到唯一的规范主体 ID。
- 共享派生物只按目标主体贡献的叶子记录重算，不删除其他主体记录。
- 已发布内容只能生成“请求外部撤回/发布修正版”的动作；不能假装外部副本已经被第三方删除。
- 统计重算只是操作性实验，不构成形式隐私证明、差分隐私证明或再识别风险证明。

## 数据定义

### `Subject`

| 参数 | 类型 | 含义 |
| --- | --- | --- |
| `canonical_id` | `str` | 主体的唯一规范 ID。 |
| `aliases` | `tuple[str, ...]` | 同一主体的显式别名；别名不能映射多个主体，也不能撞其他规范 ID。 |
| `synthetic` | `bool` | 必须为 `True`。 |

### `Artifact`

| 参数 | 类型 | 含义 |
| --- | --- | --- |
| `artifact_id` | `str` | 产物唯一 ID。 |
| `kind` | `ArtifactKind` | `raw_record`、`cache` 或 `published_stat`。 |
| `owner_subject_id` | `str \| None` | 原始记录的所属主体；派生物必须为 `None`。 |
| `synthetic` | `bool` | 必须为 `True`。 |
| `source_ids` | `tuple[str, ...]` | 上游血缘；派生物必须非空，原始记录必须为空。 |
| `formula` | `str \| None` | 派生公式，支持 `count`、`sum`、`average`。 |
| `parameters` | `Mapping[str, Any]` | 产物的扩展参数；当前核心公式不额外依赖它。 |
| `external_ref` | `str \| None` | 已发布统计的外部位置；只有 `published_stat` 可设置。 |
| `value` | `Any` | 合成记录值或当前合成统计值。 |

血缘方向为 `source_ids -> Artifact`，目录会检查缺失引用、环和重复 ID。

## 计划动作

| 动作 | 适用对象 | 语义 |
| --- | --- | --- |
| `delete_raw_record` | 目标主体的原始记录 | 计划删除的最小数据单元；其他主体的原始记录永不进入该动作。 |
| `invalidate_sole_cache` | 仅由目标主体贡献的缓存 | 删除/失效缓存派生结果；由于没有其他主体数据，不生成空统计。 |
| `recompute_shared_derived` | 多主体共享缓存 | 排除目标主体叶子记录后投影出新值，保留其他主体数据。 |
| `request_external_retraction` | 已发布统计 | 向外部位置发起撤回请求；结果不被视为已收回。 |
| `publish_corrected_stat` | 仍有其他主体贡献的已发布统计 | 在撤回请求之后发布修正统计；若只剩目标主体则不发布修正版。 |

动作顺序按依赖关系排列：先处理下游派生物，最后列出目标主体原始记录删除动作。这是执行计划的建议顺序；当前代码不会执行这些动作。

## 运行只读样例

```bash
python3.11 -m lineage_planner --subject A-001
```

`A-001` 是合成主体 `alice` 的显式别名。样例血缘包含 `alice`、`bob`、`carol` 三个合成主体。关键试算结果：

- `alice_profile_cache`：只有 Alice 的两条记录，动作是 `invalidate_sole_cache`。
- `total_click_cache`：共享缓存，`10 -> 7`，动作是 `recompute_shared_derived`。
- `average_click_published`：共享已发布统计，`2.5 -> 3.5`，动作是先请求撤回，再发布修正版。
- `total_click_published`：共享已发布统计，`10 -> 7`，动作是先请求撤回，再发布修正版。
- `alice_click_1`、`alice_click_2`：唯一被列入删除动作的原始记录。
- 输出中始终包含 `"dry_run": true` 和两条免责警告。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

测试覆盖别名映射、共享缓存重算、独有缓存失效、已发布统计撤回/修正、只读不变性、非合成数据拒绝、别名冲突、原始血缘非法输入和 JSON 序列化。

## 文件

- `lineage_planner/model.py`：合成主体、产物、计划动作模型与目录校验。
- `lineage_planner/planner.py`：DAG 血缘闭包、投影重算和只读计划生成。
- `lineage_planner/sample.py`：内置合成样例。
- `lineage_planner/__main__.py`：只读 CLI。
- `tests/test_lineage_planner.py`：标准库 `unittest` 测试。
