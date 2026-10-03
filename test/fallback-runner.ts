/**
 * node:test 的最小兼容执行器，仅在 Node < 18（无内置 node:test）时使用。
 * 测试在导入期注册，导入完成后通过 setImmediate 依次执行，失败置 exitCode=1。
 */
declare const process: { exitCode?: number };
declare function setImmediate(fn: () => void): void;

type TestFn = () => void | Promise<void>;

interface Case {
  name: string;
  fn: TestFn;
}

const queue: Case[] = [];
let scheduled = false;

export function test(name: string, fn: TestFn): void {
  queue.push({ name, fn });
  schedule();
}

export const describe = test;
export const it = test;

function schedule(): void {
  if (scheduled) return;
  scheduled = true;
  setImmediate(() => {
    void run();
  });
}

async function run(): Promise<void> {
  let passed = 0;
  let failed = 0;
  while (queue.length > 0) {
    const c = queue.shift() as Case;
    try {
      await c.fn();
      passed++;
      console.log(`ok ${passed + failed} - ${c.name}`);
    } catch (err) {
      failed++;
      console.error(`not ok ${passed + failed} - ${c.name}`);
      console.error(err);
    }
  }
  console.log(`\n# tests ${passed + failed}`);
  console.log(`# pass ${passed}`);
  console.log(`# fail ${failed}`);
  if (failed > 0) process.exitCode = 1;
}
