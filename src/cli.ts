#!/usr/bin/env node
/** 命令行入口：node src/cli.ts <新版.json> <旧版对端.json> [--capacity N] */
import { readFileSync } from 'node:fs';
import { compile, type ProtocolSpec } from './model.ts';
import { checkCompat, type CheckResult, type GlobalState, type Move } from './checker.ts';

function usage(): never {
  console.error('用法: node src/cli.ts <新版协议.json> <旧版对端.json> [--capacity N=2]');
  process.exit(2);
}

const args = process.argv.slice(2);
let capacity = 2;
const files: string[] = [];
for (let i = 0; i < args.length; i++) {
  if (args[i] === '--capacity') {
    capacity = Number(args[++i]);
    if (!Number.isInteger(capacity) || capacity < 1) {
      console.error('--capacity 必须是 >= 1 的整数');
      process.exit(2);
    }
  } else if (args[i] === '-h' || args[i] === '--help') {
    usage();
  } else {
    files.push(args[i]);
  }
}
if (files.length !== 2) usage();

function load(path: string): ProtocolSpec {
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch (e) {
    console.error(`无法读取 ${path}: ${(e as Error).message}`);
    process.exit(2);
  }
}

let result: CheckResult;
try {
  const newSide = compile(load(files[0]));
  const oldPeer = compile(load(files[1]));
  result = checkCompat(newSide, oldPeer, { capacity });
} catch (e) {
  console.error(`协议描述无效: ${(e as Error).message}`);
  process.exit(2);
}

// ---------- 输出 ----------
const fmtQ = (q: string[]): string => (q.length === 0 ? '[]' : `[${q.join(' ')}]`);
const fmtState = (s: GlobalState): string =>
  `A=${s.a} B=${s.b} A→B=${fmtQ(s.ab)} B→A=${fmtQ(s.ba)}`;
const fmtMove = (m: Move): string => {
  const act = m.kind === 'send' ? `!${m.msg}` : m.kind === 'recv' ? `?${m.msg}` : 'τ';
  return `[${m.side}] ${act}  ${m.from} -> ${m.to}`;
};

function printTrace(r: CheckResult): void {
  console.log(`  初始: ${fmtState(r.initial)}`);
  r.trace.forEach((step, i) => {
    console.log(`  ${String(i + 1).padStart(3)}. ${fmtMove(step.move).padEnd(28)} ${fmtState(step.after)}`);
  });
}

console.log(`组合: A=${files[0]} (新版)  ×  B=${files[1]} (旧版对端)，队列容量=${capacity}`);
console.log(`已探索全局状态 ${result.stats.states} 个，迁移 ${result.stats.transitions} 条，正常终止状态 ${result.stats.terminals} 个`);
console.log();

if (result.ok) {
  console.log('结论: 兼容 ✔  新版可与旧版对端完成全部规定会话（无死锁、无意外接收、所有会话均可正常终止）');
  if (result.sampleSession) {
    console.log();
    console.log('一条最短正常终止会话:');
    printTrace({ ...result, trace: result.sampleSession });
  }
  process.exit(0);
}

const issue = result.issue!;
if (issue.type === 'unspecified-reception') {
  const who = issue.side === 'A' ? '新版 A' : '旧版对端 B';
  console.log(`结论: 不兼容 ✘  意外接收（最短轨迹 ${result.trace.length} 步）`);
  console.log(`  ${who} 在状态 ${issue.localState} 收到队首消息 "${issue.msg}"，但此处只能接收: [${issue.expected.join(', ') || '（无）'}]`);
} else if (issue.type === 'deadlock') {
  const kinds = {
    'both-waiting': '双方都在等待对方消息',
    'buffer-full': '队列已满，发送被阻塞',
    'stuck': '状态机卡死',
  } as const;
  console.log(`结论: 不兼容 ✘  死锁 —— ${kinds[issue.kind]}（最短轨迹 ${result.trace.length} 步）`);
  console.log(`  A(新版): ${issue.aBlocker}`);
  console.log(`  B(旧版): ${issue.bBlocker}`);
} else {
  console.log(`结论: 不兼容 ✘  活锁（最短轨迹 ${result.trace.length} 步）`);
  console.log(`  ${issue.detail}`);
}
console.log();
console.log('问题轨迹:');
printTrace(result);
process.exit(1);
