# schemamigrate

纯 Python 3.11 标准库实现的版本化 schema 演进与批次迁移。不调用任何现成查询引擎；
整数用 Python `int`（任意精度，精确计算），十进制用 `decimal.Decimal`，
`NULL` 是独立哨兵单例，与 `NaN` 严格不同。测试使用 `unittest`。

## 功能

- **版本模式变更**：新增可空列（填 `NULL`）、删除列、列重命名、类型扩大
  （`int -> decimal` 精确）；旧批次自动投影到新模式。
- **拒绝歧义与有损隐式转换**：两个源列映射到同名目标、重命名撞上现存列、
  `decimal -> int` 无显式策略、收紧可空性、不支持的类型变更，一律在规划期拒绝。
- **显式有损转换带逐值诊断**：`decimal -> int` 需显式策略
  （`TRUNCATE` / `ROUND_HALF_UP` / `REJECT_ON_LOSS`），每个有损值产生一条
  `ValueDiagnostic(row, column, original, converted, detail)`。
- **列身份**：`column_id` 贯穿重命名链，改名不丢身份、不丢数据。
- **三方合并**：按身份匹配合并分支 schema；同一身份经不同重命名链必须汇合，
  分叉即冲突；同名新列不会吞并旧列身份；删改冲突、同名新列冲突均被拒绝。
- **迁移路径一致性**：对有限类型域逐类型采样（含 `NULL`、`NaN`、大整数），
  笛卡尔积生成候选批次，验证两条迁移路径值等价；不等价时返回首个反例批次。
- **原子发布**：`Repository.publish` 先在内存中完成全部校验与投影，
  全部成功才一次性切换 schema + 批次；任何错误都不留下部分新列或半迁移批次。

## 结构

- `schemamigrate/types.py` — 类型域、`NULL` 哨兵、扩大/显式转换规则、值相等
- `schemamigrate/schema.py` — `ColumnDef`（身份+名字+类型+可空）、`Schema`
- `schemamigrate/batch.py` — 带类型的批次，严格校验，精确整数派生列
- `schemamigrate/migration.py` — 迁移步骤、`plan_migration`、`plan_renames`
- `schemamigrate/merge.py` — 分支 schema 三方合并
- `schemamigrate/equivalence.py` — 有限域反例生成与路径等价检验
- `schemamigrate/repository.py` — 原子发布仓库
- `demo.py` — 运行样例；`tests/` — 44 个 unittest 用例

## 运行

```sh
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```

## 运行样例输出（节选）

```
2. 版本模式：新增可空列 / 重命名 / 类型扩大 / 删除；旧批次投影
迁移步骤: ['RenameColumn', 'WidenType', 'DropColumn', 'AddColumn']
  {'id': 1, 'full_name': 'ann', 'score': Decimal('9223372036854775815'), 'rating': Decimal('4.5'), 'email': NULL}
身份保持: full_name 与 name 同一 column_id: True
int->decimal 精确: True

3. decimal -> int 必须显式策略；有损显式转换保留逐值诊断
拒绝隐式有损转换: column 'rating': decimal -> int is lossy; an explicit policy is required
逐值诊断:
  row=0 column='rating' truncate: Decimal('4.5') -> 4
  row=2 column='rating' truncate: Decimal('2.5') -> 2

5. 分支 schema 三方合并
重命名链汇合 (a->x 与 a->x): Schema(v0, [x:int#A, b:string?#B])
重命名链分叉被拒绝: column 'a': name changed divergently (base='a', left='x', right='y')
同名新列不吞并旧列: 旧身份 B -> b2 , 新列 B-NEW -> b

6. 迁移路径一致性：有限类型域上生成反例验证值等价
先重命名后扩大 vs 先扩大后重命名: True - value-equivalent over 2 generated batches (row_cap=256)
TRUNCATE vs ROUND_HALF_UP: False - row 1, column 'r': 1 != 2
反例输入值: (Decimal('0'), Decimal('1.5'), Decimal('-2.5'), Decimal('0.1'))

7. 批次与 schema 原子发布：错误不留部分新列
发布被拒绝: lossy conversion at row 0, column 'rating': Decimal('4.5') -> int rejected by policy
schema 仍是 v2: True
无部分新列/部分迁移: True
```

## 测试结果

```
Ran 44 tests in 0.003s
OK
```
