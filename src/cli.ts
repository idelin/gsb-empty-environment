/**
 * 命令行入口：
 *   node src/cli.ts <input.json>     从文件读取
 *   node src/cli.ts                  从标准输入读取
 *
 * 输入 JSON 格式：
 * {
 *   "rules":   [{ "id": "r1", "action": "allow", "when": { "user": "alice" } }, ...],
 *   "samples": [{ "id": "s1", "expect": "deny", "attrs": { "user": "bob" } }, ...]
 * }
 *
 * 退出码：0 = 有解；1 = 无解（输出冲突样例子集）；2 = 输入错误。
 */

import { readFileSync } from 'node:fs';
import { solve } from './sorter.ts';
import type { Problem } from './sorter.ts';

async function readStdin(): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) chunks.push(chunk as Buffer);
  return Buffer.concat(chunks).toString('utf8');
}

async function main(): Promise<number> {
  const arg = process.argv[2];
  let raw: string;
  try {
    raw = arg ? readFileSync(arg, 'utf8') : await readStdin();
  } catch (err) {
    console.error(`读取输入失败: ${(err as Error).message}`);
    return 2;
  }

  let problem: Problem;
  try {
    problem = JSON.parse(raw) as Problem;
  } catch (err) {
    console.error(`JSON 解析失败: ${(err as Error).message}`);
    return 2;
  }

  try {
    const result = solve(problem);
    if (result.ok) {
      console.log(`有解：最少相邻交换次数 = ${result.swaps}`);
      console.log('');
      console.log('新规则顺序（首次匹配生效）：');
      result.order.forEach((id, i) => {
        const rule = problem.rules.find((r) => r.id === id)!;
        console.log(`  ${i + 1}. ${rule.id} (${rule.action})  when=${JSON.stringify(rule.when)}`);
      });
      console.log('');
      console.log('样例命中情况：');
      for (const hit of result.hits) {
        const mark = hit.action === hit.expect ? 'OK' : 'MISMATCH';
        console.log(`  ${hit.sampleId}: 期望 ${hit.expect} -> 命中规则 ${hit.ruleId} (${hit.action}) [${mark}]`);
      }
      return 0;
    }

    console.log('无解：以下样例子集互相冲突，无法通过重排规则同时满足（样例不可修改）：');
    console.log('');
    for (const cs of result.conflictSamples) {
      const rules = cs.matchingRules.length > 0
        ? cs.matchingRules.map((r) => `${r.id}(${r.action})`).join(', ')
        : '（无任何规则匹配）';
      console.log(`  ${cs.id}: 期望 ${cs.expect}，匹配规则: ${rules}`);
    }
    console.log('');
    console.log('说明：该子集是包含极小的冲突集——去掉其中任意一个样例即可有解。');
    console.log('要解决冲突，需要调整规则本身（条件或动作），而不是重排。');
    return 1;
  } catch (err) {
    console.error(`求解失败: ${(err as Error).message}`);
    return 2;
  }
}

process.exitCode = await main();
