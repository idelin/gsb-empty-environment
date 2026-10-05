/**
 * CLI 入口。
 *
 * 用法：
 *   bun src/main.ts                              运行内置演示场景
 *   bun src/main.ts <new.json> <old.json> [--capacity N]
 *                                                检查新版协议与旧版协议的兼容性
 *
 * 默认队列容量为 2。
 */

import { readFile } from "node:fs/promises";
import { checkCompatibility, DEFAULT_CAPACITY, GlobalState } from "./checker";
import { formatVerdict, PartyNames } from "./format";
import { indexProtocol, parseProtocol, ProtocolJson } from "./protocol";
import { demoScenarios } from "./examples";

async function loadProtocol(path: string) {
  const text = await readFile(path, "utf8");
  return indexProtocol(parseProtocol(JSON.parse(text) as ProtocolJson));
}

function runPair(
  newProto: ReturnType<typeof indexProtocol>,
  oldProto: ReturnType<typeof indexProtocol>,
  capacity: number
): boolean {
  const names: PartyNames = { A: newProto.protocol.name, B: oldProto.protocol.name };
  const verdict = checkCompatibility(newProto, oldProto, capacity);
  const initial: GlobalState = {
    a: newProto.protocol.initial,
    b: oldProto.protocol.initial,
    qAB: [],
    qBA: [],
  };
  console.log(formatVerdict(verdict, initial, names));
  return verdict.kind === "compatible";
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);

  if (args.includes("--help") || args.includes("-h")) {
    console.log(
      [
        "用法：",
        "  bun src/main.ts                              运行内置演示场景",
        "  bun src/main.ts <new.json> <old.json> [--capacity N]",
        "",
        "协议 JSON 格式：",
        '  { "name": "...", "initial": "S0", "finals": ["S2"],',
        '    "transitions": [{ "from": "S0", "label": "!msg|?msg|tau", "to": "S1" }] }',
      ].join("\n")
    );
    return;
  }

  const capIdx = args.indexOf("--capacity");
  const capacity = capIdx >= 0 ? Number(args[capIdx + 1]) : DEFAULT_CAPACITY;
  if (!Number.isInteger(capacity) || capacity < 1) {
    console.error("--capacity 必须是正整数");
    process.exitCode = 2;
    return;
  }
  const positional = capIdx >= 0 ? args.filter((_, i) => i !== capIdx && i !== capIdx + 1) : args;

  if (positional.length === 0) {
    console.log(`队列容量：${capacity}\n`);
    let allOk = true;
    for (const scenario of demoScenarios) {
      console.log("=".repeat(64));
      console.log(scenario.title);
      console.log(`  ${scenario.description}`);
      console.log("-".repeat(64));
      const ok = runPair(scenario.newProtocol, scenario.oldProtocol, capacity);
      allOk = allOk && ok;
      console.log();
    }
    console.log(allOk ? "全部场景均兼容。" : "部分场景不兼容（见上方轨迹）。");
    return;
  }

  if (positional.length !== 2) {
    console.error("需要两个协议文件：<new.json> <old.json>");
    process.exitCode = 2;
    return;
  }

  const [newProto, oldProto] = await Promise.all([loadProtocol(positional[0]), loadProtocol(positional[1])]);
  console.log(`新版：${newProto.protocol.name}  旧版：${oldProto.protocol.name}  队列容量：${capacity}\n`);
  const ok = runPair(newProto, oldProto, capacity);
  if (!ok) process.exitCode = 1;
}

main().catch((err) => {
  console.error(`错误：${err instanceof Error ? err.message : String(err)}`);
  process.exitCode = 2;
});
