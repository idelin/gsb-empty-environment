// 链式校验：损坏定位到操作序号；逐故障点与独立全日志解释器对照。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { verifyChain, assertChain } from '../src/log.ts';
import { Interpreter } from '../src/interpreter.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { LogCorruptionError } from '../src/types.ts';
import { deepClone } from '../src/hash.ts';
import { buildSampleLog, randomLog, mulberry32 } from './helpers.ts';

// 确定性翻转哈希首字符（保证必变）。
const flipHash = (h: string): string => (h[0] === '0' ? '1' : '0') + h.slice(1);

test('完整日志通过链式校验', () => {
  const { records } = buildSampleLog();
  const v = verifyChain(records);
  assert.equal(v.ok, true);
  assert.equal(v.validPrefix, records.length);
});

test('删除中间记录：定位到缺失的操作序号，且不可跳过继续', () => {
  const { records } = buildSampleLog();
  const cut = 4; // 删除第 5 条（seq=5）
  const corrupted = records.filter((_, i) => i !== cut);
  const v = verifyChain(corrupted);
  assert.equal(v.ok, false);
  assert.equal(v.error?.seq, cut + 1);
  assert.match(v.error?.reason ?? '', /序号断裂/);
  // 严格模式抛错，绝不跳过损坏点继续冒充成功
  assert.throws(() => assertChain(corrupted), (e: unknown) => {
    assert.ok(e instanceof LogCorruptionError);
    assert.equal((e as LogCorruptionError).seq, cut + 1);
    return true;
  });
  // 解释器同样拒绝
  assert.throws(() => new Interpreter(corrupted).runToEnd(), LogCorruptionError);
});

test('篡改记录体：哈希校验定位到该操作序号', () => {
  const { records } = buildSampleLog();
  const corrupted = deepClone(records);
  const at = 3; // 第 4 条：tx-1 中的 kv.add 操作
  const body = corrupted[at].body;
  assert.equal(body.kind, 'op');
  if (body.kind === 'op' && body.op.kind === 'kv.add') body.op.delta = 9999;
  const v = verifyChain(corrupted);
  assert.equal(v.ok, false);
  assert.equal(v.error?.seq, at + 1);
  assert.match(v.error?.reason ?? '', /哈希/);
});

test('篡改 prev 指针：定位到该操作序号', () => {
  const { records } = buildSampleLog();
  const corrupted = deepClone(records);
  corrupted[3].prev = 'deadbeef';
  const v = verifyChain(corrupted);
  assert.equal(v.ok, false);
  assert.equal(v.error?.seq, 4);
});

test('逐故障点：每个位置注入损坏，恢复报错且有效前缀与独立解释器一致', () => {
  const rng = mulberry32(7);
  const records = randomLog(rng, 30);
  for (let i = 0; i < records.length; i++) {
    // 故障点 i：截断（等价于"此处及之后损坏/丢失"）
    const prefix = records.slice(0, i);
    const truth = fullLogInterpret(prefix);
    const itp = new Interpreter(prefix);
    itp.runToEnd();
    assert.equal(itp.summary(), truth.summary, `前缀 ${i} 的状态摘要必须与独立解释器一致`);

    // 故障点 i：篡改第 i 条 → 恢复必须在 i+1 处报错，且此前前缀可正常解释
    const corrupted = deepClone(records);
    corrupted[i].hash = flipHash(corrupted[i].hash);
    assert.throws(
      () => new Interpreter(corrupted).runToEnd(),
      (e: unknown) => e instanceof LogCorruptionError && (e as LogCorruptionError).seq === i + 1,
      `故障点 ${i + 1} 必须被定位`,
    );
    const v = verifyChain(corrupted);
    assert.equal(v.validPrefix, i);
  }
});
