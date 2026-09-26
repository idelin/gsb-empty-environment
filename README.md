# 按主体撤回的聚合物化视图

纯 Python 3.11 标准库实现(无第三方依赖)。事件流驱动的聚合物化视图,
支持修订替换、主体撤回(墓碑)、同意代次控制、精确中位数、增量与全量
重算对照、JSON 持久化重启。仓库内所有示例数据均为合成数据。

## 数据模型

事件字段:

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `subject` | string | 主体标识 |
| `event_id` | string | 事件 ID,唯一标识一次贡献 |
| `group` | string | 分组 |
| `value` | int | 整数值 |
| `revision` | int ≥ 0 | 修订号,同 `event_id` 仅保留最高修订 |
| `consent_generation` | int ≥ 0 | 同意代次 |

语义规则:

1. **修订替换**:同一 `event_id` 更高 `revision` 替换旧贡献;≤ 已存修订的重放被拒绝。
2. **主体撤回**:`revoke` 删除该主体全部贡献并留下墓碑(记录撤回时主体的最高同意代次 G);
   此后 `consent_generation ≤ G` 的事件一律拒绝,较旧事件不能复活。
3. **重新同意**:主体必须携带 `consent_generation > G` 的事件才会被重新接受。
4. **查询**:按分组(及全局)输出 `sum` / `count` / 精确中位数
   (偶数个取中间两值算术平均,内部以 `Fraction` 精确表示;整值输出整数,否则输出小数)。

## 输入输出格式

### 操作流(JSONL,每行一个操作)

```json
{"op": "event", "subject": "s1", "event_id": "e1", "group": "g1", "value": 2, "revision": 1, "consent_generation": 1}
{"op": "revoke", "subject": "s1"}
```

`process` 每行输出一条受理结果:

```json
{"line": 1, "accepted": true}
```

非法操作输出 `{"line": N, "error": "..."}` 并以退出码 2 终止。

### 查询输出

```json
{"group": "global", "sum": 3, "count": 1, "median": 3}
```

`median` 在无数据时为 `null`;`--group` 指定分组时 `group` 为分组名。

### 对照输出

`verify` 将增量维护结果与从操作日志全量重算的结果比对:

```json
{"consistent": true}
```

不一致时退出码为 1。

### 持久化文件

`--db PATH` 指定的 JSON 文件保存追加操作日志
(`{"format": "subject-revocable-view/1", "ops": [...]}`),
启动时自动加载重放,命令成功后原子写回(临时文件 + rename)。

## 命令行用法

```bash
# 处理操作流(文件或标准输入),状态持久化到 view.json
python3 materialized_view.py --db view.json process ops.jsonl
cat ops.jsonl | python3 materialized_view.py --db view.json process -

# 查询(全局 / 指定分组)
python3 materialized_view.py --db view.json query
python3 materialized_view.py --db view.json query --group g1

# 增量结果与全量重算对照
python3 materialized_view.py --db view.json verify
```

## 库用法

```python
from materialized_view import Event, MaterializedView

view = MaterializedView()
view.apply_event(Event("s1", "e1", "g1", 2, revision=1, consent_generation=1))
view.revoke("s1")
view.query()            # {"group": None, "sum": 0, "count": 0, "median": None}
view.verify()           # True:增量 == 全量重算
view.save("view.json"); MaterializedView.load("view.json")
```

## 测试

```bash
python3 -m unittest -v test_materialized_view
```

覆盖:验收边界(2/8 中位数 5、撤回计数 0、旧代次 100 重放仍 0、新代次只计 3)、
修订替换、墓碑与代次、精确中位数(含 0.5 情况)、分组合计、
2000 步随机操作的增量 vs 全量重算一致性、持久化重启、CLI 端到端。
