"""Run sample: python3.11 demo.py

Walks through every feature of schemamigrate end to end.
"""
import math
from dataclasses import replace
from decimal import Decimal

from schemamigrate import (
    NULL,
    AmbiguousMappingError,
    Batch,
    LossyConversionError,
    MergeConflict,
    Migration,
    MigrationError,
    RenameColumn,
    Repository,
    RoundingPolicy,
    Schema,
    TypeTag,
    WidenType,
    check_path_equivalence,
    make_column,
    merge_schemas,
    plan_migration,
    plan_renames,
    values_equal,
)


def section(title):
    print()
    print("=" * 70)
    print(title)
    print("=" * 70)


# ---------------------------------------------------------------- schemas
c_id = make_column("id", TypeTag.INT, nullable=False)
c_name = make_column("name", TypeTag.STRING, nullable=False)
c_score = make_column("score", TypeTag.INT, nullable=False)
c_rating = make_column("rating", TypeTag.DECIMAL)
c_legacy = make_column("legacy", TypeTag.STRING)

v1 = Schema([c_id, c_name, c_score, c_rating, c_legacy], version=1)

section("1. 带列类型的批次输入；整数精确计算（不调用现成查询引擎）")
big = 2**63 + 7
batch = Batch(
    v1,
    {
        "id": [1, 2, 3],
        "name": ["ann", "bob", "cy"],
        "score": [big, 10, -5],
        "rating": [Decimal("4.5"), NULL, Decimal("2.5")],
        "legacy": ["x", NULL, "z"],
    },
)
print(v1)
print(batch)
doubled = batch.derive_column(
    make_column("score_x2", TypeTag.INT, nullable=False),
    lambda row: row["score"] + row["score"],
)
print("派生列 score_x2:", doubled.column_values("score_x2"))
print("2**63+7 翻倍逐位精确:", doubled.column_values("score_x2")[0] == 2 * big)

section("2. 版本模式：新增可空列 / 重命名 / 类型扩大 / 删除；旧批次投影")
c_email = make_column("email", TypeTag.STRING)
v2 = Schema(
    [
        c_id,
        replace(c_name, name="full_name"),          # rename
        replace(c_score, type=TypeTag.DECIMAL),     # int -> decimal 精确扩大
        c_rating,
        c_email,                                    # 新增可空列
    ],
    version=2,
)                                                 # legacy 被删除
mig12 = plan_migration(v1, v2)
print("迁移步骤:", [type(s).__name__ for s in mig12.steps])
projected, _ = mig12.project(batch)
print(v2)
print(projected)
print("身份保持: full_name 与 name 同一 column_id:",
      projected.schema.get(c_name.column_id).name == "full_name")
print("int->decimal 精确:", projected.column_values("score")[0] == Decimal(big))

section("3. decimal -> int 必须显式策略；有损显式转换保留逐值诊断")
v3 = Schema(
    [
        c_id,
        replace(c_name, name="full_name"),
        replace(c_score, type=TypeTag.DECIMAL),
        replace(c_rating, type=TypeTag.INT),        # decimal -> int
        c_email,
    ],
    version=3,
)
try:
    plan_migration(v2, v3)
except MigrationError as exc:
    print("拒绝隐式有损转换:", exc)
mig23 = plan_migration(v2, v3, policies={c_rating.column_id: RoundingPolicy.TRUNCATE})
out3, diags = mig23.project(projected)
print("rating 转换结果:", out3.column_values("rating"))
print("逐值诊断:")
for d in diags:
    print(f"  row={d.row} column={d.column!r} {d.detail}")

section("4. 拒绝歧义映射：两个源列同名目标")
try:
    plan_renames(v1, {"name": "label", "legacy": "label"})
except AmbiguousMappingError as exc:
    print("拒绝:", exc)

section("5. 分支 schema 三方合并")
col_a = make_column("a", TypeTag.INT, nullable=False, column_id="A")
col_b = make_column("b", TypeTag.STRING, column_id="B")
base = Schema([col_a, col_b], version=1)

left = Schema([replace(col_a, name="x"), col_b])
right = Schema([replace(col_a, name="x"), col_b])
print("重命名链汇合 (a->x 与 a->x):", merge_schemas(base, left, right))

right_div = Schema([replace(col_a, name="y"), col_b])
try:
    merge_schemas(base, left, right_div)
except MergeConflict as exc:
    print("重命名链分叉被拒绝:", exc)

new_b = make_column("b", TypeTag.INT, column_id="B-NEW")
left_swap = Schema([col_a, replace(col_b, name="b2"), new_b])
merged = merge_schemas(base, left_swap, base)
print("同名新列不吞并旧列: 旧身份 B ->", merged.get("B").name,
      ", 新列 B-NEW ->", merged.get("B-NEW").name)

left_add = Schema([col_a, col_b, make_column("c", TypeTag.BOOL, column_id="C1")])
right_add = Schema([col_a, col_b, make_column("c", TypeTag.BOOL, column_id="C2")])
try:
    merge_schemas(base, left_add, right_add)
except MergeConflict as exc:
    print("两分支同名新列冲突:", exc)

section("6. 迁移路径一致性：有限类型域上生成反例验证值等价")
src = Schema([make_column("n", TypeTag.INT, nullable=False, column_id="N")])
path1 = Migration(src, [RenameColumn("N", "m"), WidenType("N", TypeTag.DECIMAL)])
path2 = Migration(src, [WidenType("N", TypeTag.DECIMAL), RenameColumn("N", "m")])
rep = check_path_equivalence(path1, path2)
print("先重命名后扩大 vs 先扩大后重命名:", rep.equivalent, "-", rep.detail)

src2 = Schema([make_column("r", TypeTag.DECIMAL, nullable=False, column_id="R")])
dst2 = Schema([make_column("r", TypeTag.INT, nullable=False, column_id="R")])
mig_t = plan_migration(src2, dst2, policies={"R": RoundingPolicy.TRUNCATE})
mig_h = plan_migration(src2, dst2, policies={"R": RoundingPolicy.ROUND_HALF_UP})
rep2 = check_path_equivalence(mig_t, mig_h)
print("TRUNCATE vs ROUND_HALF_UP:", rep2.equivalent, "-", rep2.detail)
print("反例输入值:", rep2.counterexample.column_values("r"))
print("路径1输出:", rep2.left_output.column_values("r"))
print("路径2输出:", rep2.right_output.column_values("r"))

section("7. 批次与 schema 原子发布：错误不留部分新列")
repo = Repository(v1)
repo.append(batch)
repo.publish(mig12)
print("发布 v2 成功, 当前 schema 版本:", repo.schema.version)
print("旧批次已投影, email 列:", repo.batches[0].column_values("email"))

mig23_strict = plan_migration(
    v2, v3, policies={c_rating.column_id: RoundingPolicy.REJECT_ON_LOSS}
)
try:
    repo.publish(mig23_strict)   # rating 含 4.5, 触发拒绝
except LossyConversionError as exc:
    print("发布被拒绝:", exc)
print("schema 仍是 v2:", repo.schema.version == 2)
print("无部分新列/部分迁移:", all(b.schema == repo.schema for b in repo.batches))

diags = repo.publish(
    plan_migration(v2, v3, policies={c_rating.column_id: RoundingPolicy.TRUNCATE})
)
print("改用 TRUNCATE 策略发布 v3 成功, 诊断条数:", len(diags))

section("8. null 与 NaN 不同")
c_f = make_column("f", TypeTag.FLOAT, column_id="F")
f_batch = Batch(Schema([c_f]), {"f": [NULL, float("nan"), 1.5]})
vals = f_batch.column_values("f")
print("值:", vals)
print("vals[0] is NULL:", vals[0] is NULL)
print("vals[1] is NULL:", vals[1] is NULL, "(NaN 不是 NULL)")
print("math.isnan(vals[1]):", math.isnan(vals[1]))
print("values_equal(NULL, NaN):", values_equal(vals[0], vals[1]))
