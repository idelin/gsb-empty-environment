// 差分模糊测试：逐故障点恢复 vs 独立全日志解释器。
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Interpreter } from '../src/interpreter.ts';
import { fullLogInterpret } from '../src/differential.ts';
import { recover, createCheckpoint, reclaimCheckpoints } from '../src/checkpoint.ts';
import { verifyChain } from '../src/log.ts';
import { LogCorruptionError } from '../src/types.ts';
import { deepClone } from '../src/hash.ts';
import { randomLog, mulberry32, rechain } from './helpers.ts';

const flipHash = (h: string): string => (h[0] === '0' ? '1' : '0') + h.slice(1);

test('差分：随机日志的全量/增量恢复均等于独立解释器基准', () => {
  const rng = mulberry32(1234);
  for (let round = 0; round < 50; round++) {
    const records = randomLog(rng, 30 + Math.floor(rng() * 30));
    const truth = fullLogInterpret(records);

    // 全量恢复
    const firedA: string[] = [];
    const a = recover(records, [], (e) => firedA.push(e));
    assert.equal(a.interpreter.summary(), truth.summary, `第 ${round} 轮全量恢复`);
    assert.deepEqual(firedA, truth.firedEffects, `第 ${round} 轮副作用序列`);

    // 随机检查点增量恢复（切点对齐到最近的事务提交边界）
    const upto = 1 + Math.floor(rng() * records.length);
    const pre = new Interpreter(records);
    for (let i = 0; i < upto; i++) pre.step();
    while (pre.hasOpenTx() && pre.position < records.length) pre.step();
    const cp = createCheckpoint(`cp-${round}`, pre);
    const firedB: string[] = [];
    const b = recover(records, reclaimCheckpoints([cp]), (e) => firedB.push(e));
    assert.equal(b.interpreter.summary(), truth.summary, `第 ${round} 轮增量恢复`);
    assert.deepEqual(
      [...firedB].sort(),
      [...truth.firedEffects].sort(),
      `第 ${round} 轮增量副作用集合`,
    );
  }
});

test('差分：逐故障点注入损坏，恢复必须定位且绝不冒充成功', () => {
  const rng = mulberry32(555);
  for (let round = 0; round < 10; round++) {
    const records = randomLog(rng, 25);
    for (let i = 0; i < records.length; i++) {
      // 故障一：篡改内容
      const tampered = deepClone(records);
      const body = tampered[i].body;
      if (body.kind === 'op' && body.op.kind === 'kv.add') body.op.delta += 1;
      else tampered[i].hash = flipHash(tampered[i].hash);
      assert.throws(
        () => recover(tampered, []),
        (e: unknown) => e instanceof LogCorruptionError && (e as LogCorruptionError).seq === i + 1,
        `第 ${round} 轮故障点 ${i + 1}（篡改）必须被定位`,
      );

      // 故障二：删除该条（缺中间操作不可跳过；删除末条等价于合法截断）
      const deleted = records.filter((_, j) => j !== i);
      if (i < records.length - 1) {
        assert.throws(
          () => recover(deleted, []),
          (e: unknown) => e instanceof LogCorruptionError && (e as LogCorruptionError).seq === i + 1,
          `第 ${round} 轮故障点 ${i + 1}（缺失）必须被定位`,
        );
      } else {
        const truncated = recover(deleted, []);
        assert.equal(truncated.interpreter.summary(), fullLogInterpret(deleted).summary);
      }

      // 有效前缀仍可与独立解释器对照
      const v = verifyChain(tampered);
      assert.equal(v.validPrefix, i);
      const oracle = fullLogInterpret(records.slice(0, i));
      const itp = new Interpreter(records.slice(0, i));
      itp.runToEnd();
      assert.equal(itp.summary(), oracle.summary);
    }
  }
});

test('差分：注入链上合法的重复操作，恢复不得重放两次', () => {
  const rng = mulberry32(88);
  for (let round = 0; round < 20; round++) {
    const records = randomLog(rng, 30);
    const truth = fullLogInterpret(records);
    const opIdxs = records
      .map((r, i) => ({ r, i }))
      .filter(({ r }) => r.body.kind === 'op' && r.body.tx === null)
      .map(({ i }) => i);
    if (opIdxs.length === 0) continue;
    const at = opIdxs[Math.floor(rng() * opIdxs.length)];
    const dup = rechain([...records.slice(0, at + 1), records[at], ...records.slice(at + 1)]);
    const itp = new Interpreter(dup);
    itp.runToEnd();
    assert.equal(itp.summary(), truth.summary, `第 ${round} 轮重复操作不得二次生效`);
  }
});
