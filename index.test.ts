import assert from "node:assert/strict";
import { once } from "node:events";
import { test } from "node:test";
import { Worker } from "node:worker_threads";
import {
  SnapshotValidationError,
  readAndValidate,
  readSnapshot,
  registerRange,
  registerView,
} from "./index.ts";

function startWorker(code: string, workerData: Record<string, unknown>): Worker {
  return new Worker(code, { eval: true, workerData });
}

test("登记范围：越界、零长度、偏移溢出", () => {
  const sab = new SharedArrayBuffer(8);

  assert.throws(() => registerRange(sab, 0, 9), RangeError);
  assert.throws(() => registerRange(sab, 8, 1), RangeError);
  assert.throws(() => registerRange(sab, -1, 1), RangeError);
  assert.throws(() => registerRange(sab, 0, -1), RangeError);
  assert.throws(() => registerRange(sab, 1.5, 1), RangeError);
  assert.throws(() => registerRange(sab, 0, Number.NaN), RangeError);

  // 偏移溢出：用减法判断，巨大的偏移不会被加法溢出绕过
  assert.throws(() => registerRange(sab, Number.MAX_SAFE_INTEGER, 1), RangeError);
  assert.throws(() => registerRange(sab, 2 ** 53, 0), RangeError);
  assert.throws(() => registerRange(sab, Number.MAX_VALUE, 1), RangeError);
  assert.throws(() => registerRange(sab, Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER), RangeError);

  assert.throws(() => registerRange("not a buffer" as never), TypeError);

  // 零长度：偏移在 [0, byteLength] 内合法，超出即越界
  assert.equal(readSnapshot(registerRange(sab, 0, 0)).byteLength, 0);
  assert.equal(readSnapshot(registerRange(sab, 8, 0)).byteLength, 0);
  assert.throws(() => registerRange(sab, 9, 0), RangeError);

  const accepted = readAndValidate(registerRange(sab, 8, 0), (s) => s.byteLength === 0);
  assert.equal(accepted.byteLength, 0);
});

test("registerView：正常视图登记，越界视图拒绝", () => {
  const buf = new ArrayBuffer(16, { maxByteLength: 16 });
  const view = new Uint8Array(buf, 4, 8);
  const range = registerView(view);
  assert.equal(range.byteOffset, 4);
  assert.equal(range.byteLength, 8);

  buf.resize(2); // 视图越界：定长 TypedArray getter 归零，subarray 检查暴露
  assert.throws(() => registerView(view), RangeError);

  const dvBuf = new ArrayBuffer(16, { maxByteLength: 16 });
  const dv = new DataView(dvBuf, 2, 4);
  dvBuf.resize(0); // DataView 越界后 getter 直接抛错
  assert.throws(() => registerView(dv), RangeError);

  assert.throws(() => registerView({} as never), TypeError);
});

test("范围用当时的 byteLength 判断：缓冲区缩小后读取被拒绝", () => {
  const buf = new ArrayBuffer(16, { maxByteLength: 16 });
  const range = registerRange(buf, 0, 16);
  assert.equal(readSnapshot(range).byteLength, 16);

  buf.resize(8);
  assert.throws(() => readSnapshot(range), RangeError);

  buf.resize(16);
  assert.equal(readSnapshot(range).byteLength, 16);
});

test("快照是私有副本：校验后共享缓冲区被改写，业务读到的仍是校验对象", () => {
  const sab = new SharedArrayBuffer(16);
  const shared = new Uint8Array(sab);
  shared.fill(0x11);
  const range = registerRange(sab, 0, 16);

  let seenDuringValidate: Uint8Array | null = null;
  const snapshot = readAndValidate(range, (s) => {
    seenDuringValidate = s.slice();
    // 模拟攻击者在校验通过的瞬间改写共享缓冲区
    shared.fill(0xEE);
    return s.every((b) => b === 0x11);
  });

  assert.equal(shared[0], 0xEE); // 共享缓冲区确实已被改写
  assert.deepEqual(snapshot, seenDuringValidate); // 业务拿到的仍是校验过的内容
  assert.ok(snapshot.every((b) => b === 0x11));
  assert.notEqual(snapshot.buffer, sab);
});

test("另一线程在校验与复制之间持续改写内容，结果仍与校验对象一致", async () => {
  const sab = new SharedArrayBuffer(64);
  const stop = new SharedArrayBuffer(4);
  const worker = startWorker(
    `
    const { parentPort, workerData } = require("node:worker_threads");
    const view = new Uint8Array(workerData.sab);
    const stop = new Int32Array(workerData.stop);
    parentPort.postMessage("ready");
    let fill = 0xAA;
    while (Atomics.load(stop, 0) === 0) {
      view.fill(fill);
      for (let i = 0; i < 20000; i++) {} // 让每个取值持续一小段时间，便于主线程采样
      fill = fill === 0xAA ? 0xBB : 0xAA;
    }
    parentPort.postMessage("done");
    `,
    { sab, stop },
  );
  await once(worker, "message"); // ready

  try {
    const range = registerRange(sab, 0, 64);
    const observedValues = new Set<number>();

    for (let i = 0; i < 500; i++) {
      let seenDuringValidate: Uint8Array | null = null;
      let snapshot: Uint8Array;
      try {
        snapshot = readAndValidate(range, (s) => {
          seenDuringValidate = s.slice();
          const first = s[0];
          for (const b of s) if (b !== first) return false; // 复制瞬间抓到混合内容则拒绝
          return true;
        });
      } catch (err) {
        assert.ok(err instanceof SnapshotValidationError);
        continue; // 复制瞬间抓到混合内容会被校验拒绝，属预期
      }
      // 核心不变量：业务读到的快照与校验时看到的对象逐字节一致
      assert.deepEqual(snapshot, seenDuringValidate);
      assert.ok(snapshot.every((b) => b === snapshot[0]));
      observedValues.add(snapshot[0]);
    }

    // 两种取值都被观测到，证明并发改写确实作用在我们读取的缓冲区上
    assert.deepEqual([...observedValues].sort(), [0xAA, 0xBB]);
  } finally {
    Atomics.store(new Int32Array(stop), 0, 1);
    await once(worker, "exit");
  }
});

test("另一线程在校验与复制之间增长缓冲区，快照长度仍等于登记长度", async () => {
  const sab = new SharedArrayBuffer(8, { maxByteLength: 4096 });
  const control = new SharedArrayBuffer(4); // 0=待命 1=增长 2=停止
  const worker = startWorker(
    `
    const { parentPort, workerData } = require("node:worker_threads");
    const control = new Int32Array(workerData.control);
    parentPort.postMessage("ready");
    while (Atomics.load(control, 0) === 0) {}
    while (Atomics.load(control, 0) === 1) {
      const cur = workerData.sab.byteLength;
      if (cur < workerData.sab.maxByteLength) workerData.sab.grow(cur + 8);
      new Uint8Array(workerData.sab).fill(0x5A);
    }
    parentPort.postMessage("done");
    `,
    { sab, control },
  );
  await once(worker, "message"); // ready

  try {
    // 当时 byteLength = 8（worker 待命未增长）：登记 (0, 64) 必须被拒绝
    assert.equal(sab.byteLength, 8);
    assert.throws(() => registerRange(sab, 0, 64), RangeError);
    const range = registerRange(sab, 0, 8);

    Atomics.store(new Int32Array(control), 0, 1); // 通知 worker 开始增长与改写
    for (let i = 0; i < 300; i++) {
      const snapshot = readAndValidate(range, (s) => s.every((b) => b === 0x5A || b === 0x00));
      assert.equal(snapshot.byteLength, 8); // 长度不随后台 grow 改变
    }
    while (sab.byteLength < sab.maxByteLength) {} // 等 worker 增长到上限
  } finally {
    Atomics.store(new Int32Array(control), 0, 2);
    await once(worker, "exit");
  }

  // 增长完成后，用当时的 byteLength 重新判断：现在 (0, 64) 合法
  assert.equal(sab.byteLength, 4096);
  assert.equal(readSnapshot(registerRange(sab, 0, 64)).byteLength, 64);
});
