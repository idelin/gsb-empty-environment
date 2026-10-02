import { TemporalDelegationKernel } from "../src/kernel.ts";

const INF = Number.POSITIVE_INFINITY;

const kernel = new TemporalDelegationKernel();
kernel.registerRole({ id: "R", permissions: new Set(["doc:read", "doc:write"]) });

// A 直接持有角色 R（长期授权，知悉于 0）
kernel.grantRole("A", "R", 0, INF, 0);
// A 在知悉时刻 10 将 R 委托给 B，业务时间 [10, 50)
kernel.delegate("A", "B", "R", 10, 50, 10);
// B 在知悉时刻 20 将 R 转委托给 C，业务时间 [20, 60)
kernel.delegate("B", "C", "R", 20, 60, 20);
// A 在知悉时刻 30 提前撤回对 B 的委托，业务生效时刻 25
kernel.revokeDelegation("A", "B", "R", 25, 30);

function show(user: string, businessTime: number, auditTime: number): void {
  const permissions = kernel.effectivePermissions(user, businessTime, auditTime);
  const verdict = permissions.length > 0 ? `有效 { ${permissions.join(", ")} }` : "已失效（无权限）";
  console.log(
    `用户 ${user} | 业务时间 ${String(businessTime).padStart(2)} | 知悉时间 ${String(auditTime).padStart(2)} -> ${verdict}`,
  );
}

console.log("--- 验收场景：A->B [10,50)，B->C [20,60)，A 于知悉 30 撤回（业务生效 25） ---");
show("C", 28, 25); // 撤回尚未知悉：链上各环均有效
show("C", 28, 35); // 验收点：知悉 35 查询业务 28，已失效
show("C", 24, 35); // 撤回自业务 25 起生效，业务 24 仍有效
show("B", 28, 35); // 上游 B 同样已失效

console.log("\n--- 多层委托链自然过期（无撤回） ---");
const kernel2 = new TemporalDelegationKernel();
kernel2.registerRole({ id: "R", permissions: new Set(["doc:read"]) });
kernel2.grantRole("A", "R", 0, INF, 0);
kernel2.delegate("A", "B", "R", 10, 50, 10);
kernel2.delegate("B", "C", "R", 20, 60, 20);
const q2 = (user: string, bt: number) =>
  console.log(
    `用户 ${user} | 业务时间 ${bt} | 知悉时间 100 -> ${kernel2.hasRole(user, "R", bt, 100) ? "有效" : "已失效（无权限）"}`,
  );
q2("C", 49); // 上游 [10,50) 仍覆盖
q2("C", 55); // 上游已过期，下游 [20,60) 随之失效
