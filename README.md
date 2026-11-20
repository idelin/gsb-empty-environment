# Plan admission for tenant-policy rewrites

这是一个可导入的 TypeScript 计划准入组件。它比较

- 参考计划在租户读取策略下的结果多重集，和
- 候选计划在同一数据库上的结果多重集，

只有当候选计划在完整的两表 bag 语义边界内等价时，才为候选计划摘要和策略版本签发执行凭据。

## 语义

- 计划节点：`scan`、两表 `inner`/`left` join、`filter`、`project`、`unionAll`。
- 表达式：SQL 三值 `=`、`IS NULL`、`AND`、`OR`、`NOT`。
- 结果使用 bag（多重集）语义，`UNION ALL` 和投影都不去重。
- 策略在表扫描处生效。隐藏行不参与 join、filter、projection 或 UNION ALL；
  对 LEFT JOIN 而言，隐藏的右行等同于右表没有匹配行，因此仍产生全 NULL 扩展行。
- 候选计划默认在完整数据库（包括隐藏行）上求值。若宿主希望候选计划也由另一策略过滤，
  可传入 `candidatePolicy`。

## 快速开始

```ts
import {
  admitPlan,
  column,
  enumConst,
  eq,
  filter,
  innerJoin,
  project,
  projection,
  scan,
} from "./src/index.ts";

const a = scan("A", "a");
const b = scan("B", "b");
const on = eq(column("a", "id"), column("b", "id"));

const reference = project(innerJoin(a, b, on), [
  projection("id", column("a", "id")),
]);

const candidate = project(
  innerJoin(
    filter(a, eq(column("a", "tenant"), enumConst("t1"))),
    filter(b, eq(column("b", "tenant"), enumConst("t1"))),
    on,
  ),
  [projection("id", column("a", "id"))],
);

const result = admitPlan({
  reference,
  candidate,
  policy: {
    version: "policy-2026-11-20",
    subject: "alice",
    allowedTenants: ["t1"],
    tenantColumn: "tenant",
  },
});

if (result.admitted) {
  console.log(result.credential);
}
```

`admitPlan` 也接受位置参数：`admitPlan(reference, candidate, policy, schema?)`。
常用别名：`admit`、`admitQueryPlan`、`authorizePlan`、`checkPlanAdmission`、
`checkPlanEquivalence`、`plansEquivalent`。

## 策略形式

```ts
{
  version: "v1",                 // 未提供时使用策略摘要
  subject: "alice",
  allowedTenants: ["t1"],        // 或 visibleTenants / tenants
  tenantColumn: "tenant",        // 或 tenantColumns: { A: "tenant" }
  // grants: { alice: ["t1"] },
  // rules: [{ subject: "alice", tenant: "t1", allow: true }],
  // canRead(subject, tenant, { table, row }) { ... },
  // rowVisible(row, { subject, table }) { ... },
}
```

策略为 `allowAll: true` 时所有行可见。没有任何限制项的普通策略也按全可见处理；
`denyAll: true` 则全部隐藏。未知或缺失的租户值默认隐藏，可用 `allowNullTenant` 放开。

## 值模型

```ts
import { enumValue, idValue, NULL } from "./src/index.ts";

enumValue("active");    // 有限枚举值
idValue("account-42");  // 相等性标识；实际取值域无限，但只作相等比较
NULL;
```

数据库中的普通字符串按有限枚举值处理。标识按名字具有固定身份：同名的两个标识相等，
不同名标识不等。计划常量可用 `enumConst("active")` 或 `idConst("account-42")` 构造。

## 完备代表数据库与证书

`admitPlan` 返回 `certificate`：

- 每表 0、1、2 行全部覆盖；
- 单元格覆盖 NULL、计划/策略中出现的有限枚举值、无限标识域中的等值类；
- 使用 canonical first-use 编号覆盖跨表/跨行的标识相等与不等关系；
- 重复行、空表、全 NULL 行和 LEFT JOIN 全 NULL 扩展均可在代表类中找到；
- 不使用随机采样。

独立覆盖检查器：

```ts
import { checkPartitionCertificate } from "./src/index.ts";

const check = checkPartitionCertificate(result.certificate!, {
  reference,
  candidate,
  policy,
  checkVerdict: true,
});
if (!check.ok) throw new Error(check.errors.join("; "));
```

`checkPartitionCertificate` 会从证书声明的商域重新生成全部 canonical 类，
与证书中的类集合逐项比较；传入计划时还会重新推导域，并可重新求值证书结论。

## 边界与拒绝

- 只接受两表 INNER/LEFT JOIN 片段；三层 join、RIGHT/FULL join、聚合等会被拒绝。
- `admitPlan` 在代表类上限（默认 300,000）被超过时拒绝准入，而不是近似放行。
- 失败结果包含 `counterexample`，其中给出代表数据库、参考结果、候选结果以及缺失/多余的多重集。

## 测试

```bash
node --experimental-strip-types --test test/*.test.ts
```

测试覆盖：相同输出行数但重复次数不同、ON/WHERE 谓词移动、隐藏右行改变 LEFT JOIN
补空结果、无实际权限差别的策略重写，以及证书缺类/篡改检测。
