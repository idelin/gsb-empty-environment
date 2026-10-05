import {
  GrantResult,
  Role,
  resolveEffectivePermissions,
  solveGrant,
} from "../src/solver";

let failures = 0;

function check(cond: boolean, message: string): void {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${message}`);
  }
}

function expectOk(result: GrantResult, message: string) {
  check(result.status === "ok", `${message} (expected ok, got ${JSON.stringify(result)})`);
  if (result.status !== "ok") throw new Error("unreachable");
  return result;
}

function expectUnsat(result: GrantResult, message: string) {
  check(result.status === "unsat", `${message} (expected unsat, got ${JSON.stringify(result)})`);
  if (result.status !== "unsat") throw new Error("unreachable");
  return result;
}

function sameSet(actual: string[], expected: string[], message: string): void {
  const a = actual.slice().sort();
  const e = expected.slice().sort();
  check(
    a.length === e.length && a.every((v, i) => v === e[i]),
    `${message} (expected [${e}], got [${a}])`
  );
}

// 1. 基本：优先选角色数最少的组合。
{
  const roles: Role[] = [
    { name: "admin", permissions: ["a", "b", "c"] },
    { name: "ra", permissions: ["a"] },
    { name: "rb", permissions: ["b"] },
    { name: "rc", permissions: ["c"] },
  ];
  const r = expectOk(solveGrant({ roles, required: ["a", "b", "c"] }), "basic min count");
  sameSet(r.roles, ["admin"], "basic: single role beats three");
  sameSet(r.excess, [], "basic: no excess");
}

// 2. 角色数相同，选多余权限更少者。
{
  const roles: Role[] = [
    { name: "broad", permissions: ["a", "b", "x1", "x2", "x3"] },
    { name: "tight", permissions: ["a", "b", "x1"] },
  ];
  const r = expectOk(solveGrant({ roles, required: ["a", "b"] }), "excess tiebreak");
  sameSet(r.roles, ["tight"], "excess: tighter role wins");
  sameSet(r.excess, ["x1"], "excess: minimal excess");
}

// 3. 贪心陷阱：贪心先拿最大集合需 3 个角色，最优解 2 个。
{
  const roles: Role[] = [
    { name: "greedy-bait", permissions: ["p1", "p2", "p3", "p4"] },
    { name: "left", permissions: ["p1", "p2", "p5"] },
    { name: "right", permissions: ["p3", "p4", "p6"] },
  ];
  const required = ["p1", "p2", "p3", "p4", "p5", "p6"];
  const r = expectOk(solveGrant({ roles, required }), "greedy trap");
  check(r.roles.length === 2, `greedy trap: optimal uses 2 roles, got ${r.roles.length}`);
  sameSet(r.roles, ["left", "right"], "greedy trap: exact optimal pair");
}

// 4. 敏感权限：全覆盖角色含敏感权限被排除，改用更多角色的安全组合。
{
  const roles: Role[] = [
    { name: "superuser", permissions: ["a", "b", "c", "danger"] },
    { name: "ra", permissions: ["a"] },
    { name: "rb", permissions: ["b"] },
    { name: "rc", permissions: ["c"] },
  ];
  const r = expectOk(
    solveGrant({ roles, required: ["a", "b", "c"], sensitive: ["danger"] }),
    "sensitive avoidance"
  );
  sameSet(r.roles, ["ra", "rb", "rc"], "sensitive: safe trio chosen");
  check(r.granted.indexOf("danger") === -1, "sensitive: danger not granted");
}

// 5. 继承：有效权限为传递闭包。
{
  const roles: Role[] = [
    { name: "base", permissions: ["read"] },
    { name: "editor", permissions: ["write"], inherits: ["base"] },
    { name: "admin", permissions: ["delete"], inherits: ["editor"] },
  ];
  const eff = resolveEffectivePermissions(roles);
  sameSet(Array.from(eff.get("admin")!), ["read", "write", "delete"], "inheritance closure");
  const r = expectOk(
    solveGrant({ roles, required: ["read", "write"] }),
    "inheritance solve"
  );
  sameSet(r.roles, ["editor"], "inheritance: editor covers read+write");
}

// 6. 继承环与未知引用要报错。
{
  let threw = false;
  try {
    resolveEffectivePermissions([
      { name: "a", permissions: [], inherits: ["b"] },
      { name: "b", permissions: [], inherits: ["a"] },
    ]);
  } catch (e) {
    threw = /cycle/i.test(String(e));
  }
  check(threw, "inheritance cycle detected");

  threw = false;
  try {
    resolveEffectivePermissions([{ name: "a", permissions: [], inherits: ["ghost"] }]);
  } catch (e) {
    threw = /Unknown role/.test(String(e));
  }
  check(threw, "unknown inherited role detected");

  threw = false;
  try {
    resolveEffectivePermissions([
      { name: "a", permissions: [] },
      { name: "a", permissions: [] },
    ]);
  } catch (e) {
    threw = /Duplicate/.test(String(e));
  }
  check(threw, "duplicate role name detected");
}

// 7. 无解：缺失权限见证。
{
  const r = expectUnsat(
    solveGrant({
      roles: [{ name: "ra", permissions: ["a"] }],
      required: ["a", "missing"],
    }),
    "missing permission"
  );
  sameSet(r.missingPermissions, ["missing"], "missing witness");
  check(r.forcedSensitive.length === 0, "no forced sensitive when simply missing");
}

// 8. 无解：被迫敏感权限见证（所需权限只能由携带敏感权限的角色授予）。
{
  const r = expectUnsat(
    solveGrant({
      roles: [
        { name: "risky", permissions: ["deploy", "root"] },
        { name: "risky2", permissions: ["deploy", "sudo"] },
        { name: "safe", permissions: ["read"] },
      ],
      required: ["read", "deploy"],
      sensitive: ["root", "sudo"],
    }),
    "forced sensitive"
  );
  check(r.forcedSensitive.length === 1, "forced sensitive: one witness");
  check(r.forcedSensitive[0].permission === "deploy", "forced sensitive: on deploy");
  sameSet(r.forcedSensitive[0].sensitivePermissions, ["root", "sudo"], "forced sensitive perms");
  sameSet(r.forcedSensitive[0].roles, ["risky", "risky2"], "forced sensitive roles");
}

// 9. 所需权限本身在敏感清单中：矛盾，按被迫敏感报告。
{
  const r = expectUnsat(
    solveGrant({
      roles: [{ name: "ra", permissions: ["a", "b"] }],
      required: ["a", "b"],
      sensitive: ["b"],
    }),
    "required intersects sensitive"
  );
  check(
    r.forcedSensitive.some((w) => w.permission === "b"),
    "conflicting required-sensitive permission reported"
  );
}

// 10. 角色数上限：默认 25，超出即无解并给出见证。
{
  const roles: Role[] = [];
  const required: string[] = [];
  for (let i = 0; i < 26; i++) {
    roles.push({ name: `r${i}`, permissions: [`p${i}`] });
    required.push(`p${i}`);
  }
  const r = expectUnsat(solveGrant({ roles, required }), "max roles exceeded");
  check(
    r.maxRolesExceeded !== undefined && r.maxRolesExceeded.maxRoles === 25,
    "maxRolesExceeded witness"
  );

  const ok = expectOk(
    solveGrant({ roles, required, maxRoles: 26 }),
    "limit raised to 26"
  );
  check(ok.roles.length === 26, "26 roles granted when allowed");
}

// 11. 空需求：零角色即最优。
{
  const r = expectOk(
    solveGrant({ roles: [{ name: "ra", permissions: ["a"] }], required: [] }),
    "empty required"
  );
  sameSet(r.roles, [], "empty required: no roles");
  sameSet(r.granted, [], "empty required: nothing granted");
}

// 12. 继承引入敏感权限同样被禁用。
{
  const r = expectUnsat(
    solveGrant({
      roles: [
        { name: "root-role", permissions: ["danger"] },
        { name: "child", permissions: ["need"], inherits: ["root-role"] },
      ],
      required: ["need"],
      sensitive: ["danger"],
    }),
    "inherited sensitive"
  );
  check(
    r.forcedSensitive.some(
      (w) => w.permission === "need" && w.sensitivePermissions.indexOf("danger") !== -1
    ),
    "inherited sensitive permission blocks child role"
  );
}

// 13. 精确性：同角色数下全局最小多余权限（非首个可行解）。
{
  const roles: Role[] = [
    { name: "c1", permissions: ["a", "junk1", "junk2", "junk3"] },
    { name: "c2", permissions: ["b", "junk4"] },
    { name: "c3", permissions: ["a"] },
    { name: "c4", permissions: ["b"] },
  ];
  const r = expectOk(solveGrant({ roles, required: ["a", "b"] }), "exact excess optimum");
  sameSet(r.roles, ["c3", "c4"], "exact: zero-excess pair found");
  sameSet(r.excess, [], "exact: zero excess");
}

// 14. 与穷举对拍：随机小规模实例验证最优性。
{
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let trial = 0; trial < 200; trial++) {
    const permCount = 2 + Math.floor(rand() * 5);
    const perms: string[] = [];
    for (let i = 0; i < permCount; i++) perms.push(`p${i}`);
    const roleCount = 1 + Math.floor(rand() * 6);
    const roles: Role[] = [];
    for (let i = 0; i < roleCount; i++) {
      const ps = perms.filter(() => rand() < 0.5);
      roles.push({ name: `r${i}`, permissions: ps });
    }
    const required = perms.filter(() => rand() < 0.8);
    const result = solveGrant({ roles, required });

    // 穷举全部子集求真实最优。
    let bestCount = Infinity;
    let bestExcess = Infinity;
    const total = 1 << roleCount;
    for (let mask = 0; mask < total; mask++) {
      const union = new Set<string>();
      let count = 0;
      for (let i = 0; i < roleCount; i++) {
        if (mask & (1 << i)) {
          count++;
          for (const p of roles[i].permissions) union.add(p);
        }
      }
      let covered = true;
      for (const p of required) if (!union.has(p)) covered = false;
      if (!covered) continue;
      let excess = 0;
      for (const p of union) if (required.indexOf(p) === -1) excess++;
      if (count < bestCount || (count === bestCount && excess < bestExcess)) {
        bestCount = count;
        bestExcess = excess;
      }
    }

    if (bestCount === Infinity) {
      check(result.status === "unsat", `brute trial ${trial}: expect unsat`);
    } else {
      check(result.status === "ok", `brute trial ${trial}: expect ok`);
      if (result.status === "ok") {
        check(
          result.roles.length === bestCount,
          `brute trial ${trial}: role count ${result.roles.length} != optimal ${bestCount}`
        );
        check(
          result.excess.length === bestExcess,
          `brute trial ${trial}: excess ${result.excess.length} != optimal ${bestExcess}`
        );
      }
    }
  }
}

if (failures > 0) {
  console.error(`${failures} check(s) failed`);
  throw new Error("test failures");
}
console.log("All tests passed");
