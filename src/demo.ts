/**
 * 演示与自测：覆盖记录、联合、函数（逆变/协变）、可缺省 vs 可空、递归类型。
 * 运行：bun run src/demo.ts
 */
import { checkReplacement, formatReport, T, type CheckResult } from "./checker";

let failed = 0;

function scenario(name: string, result: CheckResult, expectFailures: number): void {
  console.log(`=== ${name} ===`);
  console.log(formatReport(result));
  console.log();
  const actual = result.failures.length;
  if (actual !== expectFailures) {
    failed++;
    console.error(`  ‼ 断言失败：期望 ${expectFailures} 处不兼容，实际 ${actual} 处\n`);
  }
}

// 1. 兼容：宽度子类型（多出字段）+ 可缺省强化为必填
scenario(
  "1. 兼容：新记录多字段、可缺省变必填",
  checkReplacement(
    T.rec({ name: T.str, age: T.num, email: T.str }),
    T.rec({ name: T.str, age: T.num }, ["age"]),
  ),
  0,
);

// 2. 不兼容：可缺省 ≠ 必填
scenario(
  "2. 不兼容：新接口 name 可缺省，旧接口要求必填",
  checkReplacement(T.rec({ name: T.str }, ["name"]), T.rec({ name: T.str })),
  1,
);

// 3a. 不兼容：可空（键必填、值可 null）≠ 可缺省（键可不存在）
scenario(
  "3a. 不兼容：旧接口 nick 可空但键必填，新接口 nick 可缺省",
  checkReplacement(T.rec({ nick: T.str }, ["nick"]), T.rec({ nick: T.nullable(T.str) })),
  1,
);

// 3b. 不兼容：新接口值可为 null，旧接口可缺省但出现时必须为 string
scenario(
  "3b. 不兼容：新接口 nick 可空，旧接口可缺省但不允许 null",
  checkReplacement(T.rec({ nick: T.nullable(T.str) }), T.rec({ nick: T.str }, ["nick"])),
  1,
);

// 3c. 兼容：必填 string 是「可空 string」的子类型
scenario(
  "3c. 兼容：新接口 nick 必填 string，旧接口 nick 可空",
  checkReplacement(T.rec({ nick: T.str }), T.rec({ nick: T.nullable(T.str) })),
  0,
);

// 4. 不兼容：函数参数逆变 + 返回值协变（两处失败）
scenario(
  "4. 不兼容：新 handler 参数要求更窄、返回值更宽",
  checkReplacement(
    T.rec({ onEvent: T.fn([T.rec({ code: T.num, msg: T.str })], T.num) }),
    T.rec({ onEvent: T.fn([T.rec({ code: T.num })], T.str) }),
  ),
  2,
);

// 4b. 兼容：参数更宽（逆变方向正确）、返回值更窄（协变方向正确）、参数更少
scenario(
  "4b. 兼容：新 handler 参数更宽、返回值子类型、可忽略多余参数",
  checkReplacement(
    T.rec({ onEvent: T.fn([], T.rec({ code: T.num, extra: T.bool })) }),
    T.rec({ onEvent: T.fn([T.rec({ code: T.num })], T.rec({ code: T.num })) }),
  ),
  0,
);

// 5. 递归类型：通过成对访问状态终止
const env = {
  NumList: T.rec({ head: T.num, next: T.nullable(T.ref("NumList")) }),
  NumListX: T.rec({ head: T.num, next: T.nullable(T.ref("NumListX")), extra: T.bool }),
  StrList: T.rec({ head: T.str, next: T.nullable(T.ref("StrList")) }),
};

scenario(
  "5a. 兼容：递归类型与自身比较（共归纳终止）",
  checkReplacement(T.ref("NumList"), T.ref("NumList"), env),
  0,
);

scenario(
  "5b. 兼容：递归记录多字段（宽度子类型穿过递归）",
  checkReplacement(T.ref("NumListX"), T.ref("NumList"), env),
  0,
);

scenario(
  "5c. 不兼容：递归结构内部 head 类型不同",
  checkReplacement(T.ref("StrList"), T.ref("NumList"), env),
  1,
);

// 6. 联合：新类型须落入旧联合某一分支
scenario(
  "6a. 兼容：string 落入旧联合 string | number",
  checkReplacement(T.str, T.union(T.str, T.num)),
  0,
);

scenario(
  "6b. 不兼容：boolean 不属于旧联合 string | number",
  checkReplacement(T.bool, T.union(T.str, T.num)),
  1,
);

scenario(
  "6c. 不兼容：新联合的 boolean 分支不属于旧联合",
  checkReplacement(T.union(T.str, T.bool), T.union(T.str, T.num)),
  1,
);

if (failed > 0) {
  console.error(`共 ${failed} 个场景断言失败`);
  process.exit(1);
}
console.log("全部场景通过。");
