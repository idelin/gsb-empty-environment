/**
 * 示例构建规格。
 *
 * flawedDemoBuild 演示四类键缺陷：
 *  1. env:FEATURE_X / tool:compiler —— 影响产物却未进键（距离 1 反例）；
 *  2. env:LOG_LEVEL                —— 只影响日志却未进键（log-only 反例）；
 *  3. read:extra.conf              —— 条件读取：仅 NODE_ENV=prod 时读取，
 *                                     只有在 prod 上下文里翻转它才构成反例；
 *  4. env:FLAG_A + env:FLAG_B      —— 都「声明」了，但键只记录它们的和（有损编码），
 *                                     (0,1) 与 (1,0) 键相同而产物不同（距离 2 反例）。
 */

import type { Assignment, BuildSpec } from "./model.ts";

const extraConfReadable = (a: Assignment): boolean => a["env:NODE_ENV"] === "prod";

export const flawedDemoBuild: BuildSpec = {
  name: "demo-app（有缺陷的缓存键）",
  inputs: [
    { name: "env:NODE_ENV", kind: "env", domain: ["dev", "prod"] },
    { name: "env:FEATURE_X", kind: "env", domain: ["on", "off"] },
    { name: "env:LOG_LEVEL", kind: "env", domain: ["quiet", "verbose"] },
    { name: "tool:compiler", kind: "tool", domain: ["gcc-12", "gcc-13"] },
    {
      name: "read:extra.conf",
      kind: "conditional-read",
      domain: ["absent", "v1", "v2"],
      readWhen: extraConfReadable,
    },
    { name: "env:FLAG_A", kind: "env", domain: ["0", "1"] },
    { name: "env:FLAG_B", kind: "env", domain: ["0", "1"] },
  ],
  key: (a) =>
    [
      `env=${a["env:NODE_ENV"]}`,
      // 有损编码：只记录两个 flag 的和
      `flags=${Number(a["env:FLAG_A"]) + Number(a["env:FLAG_B"])}`,
    ].join("|"),
  run: (a) => {
    const extra =
      extraConfReadable(a) && a["read:extra.conf"] !== "absent"
        ? `,extra=${a["read:extra.conf"]}`
        : "";
    const artifact =
      `bin<env=${a["env:NODE_ENV"]}` +
      `,feature_x=${a["env:FEATURE_X"]}` +
      `,cc=${a["tool:compiler"]}` +
      `,flags=${a["env:FLAG_A"]}${a["env:FLAG_B"]}` +
      extra +
      ">";
    const logs = [
      `compile: ${a["tool:compiler"]} -DMODE=${a["env:NODE_ENV"]}`,
      ...(a["env:LOG_LEVEL"] === "verbose"
        ? [`debug: feature_x=${a["env:FEATURE_X"]}`, "debug: build done"]
        : []),
    ];
    return { artifact, logs };
  },
};

/** 对照组：键覆盖了全部实际使用的输入，审计应当干净 */
export const soundDemoBuild: BuildSpec = {
  ...flawedDemoBuild,
  name: "demo-app（健全的缓存键）",
  key: (a) =>
    JSON.stringify([
      a["env:NODE_ENV"],
      a["env:FEATURE_X"],
      a["env:LOG_LEVEL"],
      a["tool:compiler"],
      a["read:extra.conf"],
      a["env:FLAG_A"],
      a["env:FLAG_B"],
    ]),
};
