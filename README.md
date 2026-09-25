# schemamig

Schema 版本化 / 迁移 / 合并系统（Python 3.11，仅标准库，不依赖任何现成查询引擎）。
表 = 带列类型的批次（columnar batch）；null 与 NaN 是不同值；整数精确计算（任意精度）。

## 模块

- `schemamig/typesys.py` — 有限类型域（INT/DECIMAL/STRING/BOOL）、精确扩大规则
  （仅 int→decimal 隐式）、显式转换与逐值诊断（`Diagnostic`）。
- `schemamig/schema.py` — 列身份（`cid`）与 schema；重命名不丢身份；重名/重身份即拒绝。
- `schemamig/batch.py` — 列式批次，构造时校验类型与可空性。
- `schemamig/migration.py` — 版本模式算子：`AddColumn`（必须可空）、`DropColumn`、
  `RenameColumn`、`WidenColumn`（仅精确扩大）、`ConvertColumn`（显式，decimal→int
  必须带策略）；`apply` 校验并拒绝歧义映射（两源列同名目标）与有损隐式转换；
  `project` 把旧批次投影到新模式，纯函数、出错不留部分结果。
- `schemamig/merge.py` — 分支 schema 三方合并：按列身份匹配；同一身份经不同重命名链
  必须汇合，否则冲突；同名新列不可吞并旧列（或彼此）；drop vs modify 冲突。
  返回合并 schema 及两分支到合并点的残余迁移，供路径一致性检验。
- `schemamig/equivalence.py` — 在有限类型域上枚举样本值（含 null 与 NaN）生成小批次，
  搜索反例以验证两条迁移路径是否值等价；一方报错另一方成功也算反例。
- `schemamig/repository.py` — schema 版本仓库；`publish` 先校验并投影全部批次再单点
  交换状态，批次与 schema 发布原子，失败不留部分新列。

## 运行

```
python3.11 demo.py                          # 运行样例
python3.11 -m unittest discover -s tests -v # 测试
```

## 运行样例（`python3.11 demo.py` 摘要）

```
=== publish v2: rename + exact widening + new nullable column ===
schema: Schema(id:int#c_id, full_name:string#c_name, balance:decimal#c_bal, email:string#c_mail)
rows:   [(1, ada, Decimal(100), None), (2, bob, Decimal(-5), None), (3, None, Decimal(1180591620717411303425), None)]
identity kept: c_name -> full_name | big int exact: 1180591620717411303425

=== rejected: lossy implicit conversion (decimal -> int) ===
LossyImplicitError: decimal->int is not an exact widening; use ConvertColumn with an explicit policy

=== rejected: ambiguous mapping (two sources -> one target name) ===
AmbiguousMappingError: ambiguous mapping: multiple source columns land on [dup]

=== explicit lossy conversion keeps per-value diagnostics ===
projected: (1, -2, 7, None)
  diagnostic row 0: 1.5 -> 1 (fractional part discarded)
  diagnostic row 1: -2.25 -> -2 (fractional part discarded)
null vs NaN distinct: null = None | NaN = NaN

=== atomic publish: failure leaves no partial new columns ===
publish failed: ambiguous mapping: multiple source columns land on [balance]
no partial column extra: True

=== three-way merge of branch schemas ===
merged: Schema(id:int#c_id, display_name:string#c_name, balance:decimal#c_bal, email:string#c_mail, tag:string#c_tag)
paths base->A->merge vs base->B->merge value-equivalent: True

=== merge conflict: rename chains that do not converge ===
MergeConflict: column #c_name renamed to x and y: rename chains do not converge

=== merge conflict: new column must not swallow an old column ===
MergeConflict: new column total#c_new collides with existing column #c_bal: ...

=== counterexample: truncate vs floor are not value-equivalent ===
counterexample input rows: [(Decimal(-2.25),)]
truncate path: (-2,) | floor path: (-3,)
```

## 测试结果

`python3.11 -m unittest discover -s tests`：37 个测试全部通过（OK），覆盖：
版本模式四算子与投影、int→decimal 精确扩大、decimal→int 显式策略与逐值诊断、
重命名链身份保持、歧义映射/有损隐式拒绝、null≠NaN、三方合并（重命名链汇合/分叉、
新列吞并、drop-vs-modify）、路径值等价与反例生成、原子发布不留部分新列。
