import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { compile } from '../src/jsonpath/compiler.ts';
import { evaluate } from '../src/jsonpath/interpreter.ts';
import { runStream, streamQuery } from '../src/jsonpath/stream.ts';
import { walk } from '../src/jsonpath/events.ts';
import { canonical } from '../src/jsonpath/path.ts';
import { BufferOverflowError, CancelledError } from '../src/jsonpath/errors.ts';
import type { JsonValue } from '../src/jsonpath/ast.ts';

const doc = {
  a: [{ x: 1 }, { x: 5 }, { x: 9 }],
  b: { deep: { deeper: 'leaf' } },
  max: 4,
};

function sortedPaths(matches: { path: (string | number)[] }[]): string[] {
  return matches.map((m) => canonical(m.path)).sort();
}

test('流式结果与参考解释器一致', () => {
  const queries = [
    '$.a[*].x',
    '$.a[0].x',
    '$.a[-1].x',
    '$.a[0:2].x',
    '$.a[::-1].x',
    '$.a[?(@.x > 2)].x',
    '$.a[?(@.x > $.max)].x',
    '$..x',
    '$..*',
    '$.b.deep.deeper',
    '$.nothing[*]',
  ];
  for (const q of queries) {
    const prog = compile(q);
    const expected = sortedPaths(evaluate(doc as JsonValue, prog));
    const got = sortedPaths(streamQuery(doc as JsonValue, prog).matches);
    assert.deepEqual(got, expected, q);
  }
});

test('可提前确定的结果增量输出（在后续子树到达前）', () => {
  // $.a[*].x 的全部结果应在 b 子树开始之前输出完毕
  const events = [...walk(doc as JsonValue)];
  const bEnterIdx = events.findIndex((e) => e.type === 'enter' && e.path[0] === 'b');
  const emitEvents: number[] = [];
  streamQuery(doc as JsonValue, compile('$.a[*].x'), {
    onMatch: (_m, n) => emitEvents.push(n),
  });
  assert.equal(emitEvents.length, 3);
  for (const n of emitEvents) assert.ok(n < bEnterIdx, `emitted at ${n}, b enters at ${bEnterIdx}`);
});

test('负下标在数组闭合前不可判定', () => {
  const events = [...walk(doc as JsonValue)];
  const aExitIdx = events.findIndex((e) => e.type === 'exit' && e.path[0] === 'a');
  const emitEvents: number[] = [];
  const res = streamQuery(doc as JsonValue, compile('$.a[-1].x'), {
    onMatch: (_m, n) => emitEvents.push(n),
  });
  assert.equal(res.matches.length, 1);
  assert.equal(canonical(res.matches[0]!.path), '$["a"][2]["x"]');
  // 发出点不早于 a 闭合
  assert.ok(emitEvents[0]! >= aExitIdx, `emitted at ${emitEvents[0]}, a exits at ${aExitIdx}`);
});

test('含过滤的查询逐元素输出但尾部元素需等其完整', () => {
  const events = [...walk(doc as JsonValue)];
  const elem2ExitIdx = events.findIndex((e) => e.type === 'exit' && e.path[0] === 'a' && e.path[1] === 2);
  const emits: { path: string; at: number }[] = [];
  streamQuery(doc as JsonValue, compile('$.a[?(@.x > 2)].x'), {
    onMatch: (m, n) => emits.push({ path: canonical(m.path), at: n }),
  });
  assert.deepEqual(emits.map((e) => e.path), ['$["a"][1]["x"]', '$["a"][2]["x"]']);
  // 最后一个命中在其元素闭合之后才被判定
  assert.ok(emits[1]!.at >= elem2ExitIdx);
});

test('引用 $ 的谓词延期到根闭合后求值', () => {
  const events = [...walk(doc as JsonValue)];
  const rootExitIdx = events.length - 1;
  const emits: number[] = [];
  const res = streamQuery(doc as JsonValue, compile('$.a[?(@.x > $.max)].x'), {
    onMatch: (_m, n) => emits.push(n),
  });
  assert.deepEqual(sortedPaths(res.matches), ['$["a"][1]["x"]', '$["a"][2]["x"]']);
  for (const n of emits) assert.ok(n >= rootExitIdx);
});

test('有界暂存溢出：无溢写目录时报错并定位路径', () => {
  const big = { arr: Array.from({ length: 50 }, (_, i) => i) };
  assert.throws(() => streamQuery(big as JsonValue, compile('$.arr[-1]'), { maxBuffered: 4 }), (e: unknown) => {
    assert.ok(e instanceof BufferOverflowError);
    assert.equal(e.path, '$["arr"]');
    return true;
  });
});

test('有界暂存溢出：配置溢写目录后结果正确且临时文件被清理', () => {
  const dir = mkdtempSync(join(tmpdir(), 'jp-spill-'));
  try {
    const big = { arr: Array.from({ length: 200 }, (_, i) => ({ v: i })) };
    const res = streamQuery(big as JsonValue, compile('$.arr[-3:].v'), { maxBuffered: 8, spillDir: dir });
    assert.equal(res.spilled, true);
    assert.deepEqual(sortedPaths(res.matches), ['$["arr"][197]["v"]', '$["arr"][198]["v"]', '$["arr"][199]["v"]']);
    assert.deepEqual(readdirSync(dir), [], 'spill files must be cleaned up');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('溢写同样覆盖需要全局信息的谓词', () => {
  const dir = mkdtempSync(join(tmpdir(), 'jp-spill-'));
  try {
    const big = { arr: Array.from({ length: 120 }, (_, i) => ({ v: i })), threshold: 100 };
    const res = streamQuery(big as JsonValue, compile('$.arr[?(@.v > $.threshold)].v'), {
      maxBuffered: 4,
      spillDir: dir,
    });
    assert.equal(res.spilled, true);
    assert.equal(res.matches.length, 19);
    assert.deepEqual(readdirSync(dir), []);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('取消：抛 CancelledError 且清理临时数据', () => {
  const dir = mkdtempSync(join(tmpdir(), 'jp-spill-'));
  try {
    const big = { arr: Array.from({ length: 500 }, (_, i) => ({ v: i })), tail: 1 };
    // 第 20 个事件后取消
    const ac2 = new AbortController();
    const events = walk(big as JsonValue);
    let seen = 0;
    const gen = (function* () {
      for (const e of events) {
        if (++seen === 20) ac2.abort();
        yield e;
      }
    })();
    assert.throws(
      () => runStream(compile('$.arr[-1].v'), gen, { maxBuffered: 4, spillDir: dir, signal: ac2.signal }),
      CancelledError,
    );
    assert.deepEqual(readdirSync(dir), [], 'cancel must clean up temp files');
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('深树流式执行不爆栈', () => {
  const depth = 5000;
  let node: Record<string, unknown> = { v: 7 };
  for (let i = 0; i < depth; i++) node = { next: node };
  const res = streamQuery(node as JsonValue, compile('$..v'));
  assert.equal(res.matches.length, 1);
  assert.equal(res.matches[0]!.value, 7);
});
