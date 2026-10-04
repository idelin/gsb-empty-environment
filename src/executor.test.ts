import { describe, expect, test } from 'bun:test';
import {
  StagedJobExecutor,
  publicStatusOf,
  type Boundary,
  type JobDriver,
  type RecoveryRecord,
  type SubmissionOutcome,
} from './executor';

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

const accepted: SubmissionOutcome = { kind: 'accepted', ref: 'remote-1' };
const rejected: SubmissionOutcome = { kind: 'rejected', reason: 'quota' };

/** 收集执行器产出的每一份持久化快照。 */
function recorder() {
  const records: RecoveryRecord[] = [];
  return {
    records,
    onRecord: (r: RecoveryRecord) => records.push(r),
  };
}

function assertSerializable(rec: RecoveryRecord) {
  expect(JSON.parse(JSON.stringify(rec))).toEqual(rec);
}

describe('阶段机：正常流程', () => {
  test('prepare -> submit -> cleanup 顺序执行，最终 submitted', async () => {
    const order: string[] = [];
    const driver: JobDriver = {
      prepare: async () => { order.push('prepare'); },
      submit: async () => { order.push('submit'); return accepted; },
      cleanup: async () => { order.push('cleanup'); },
    };
    const ex = StagedJobExecutor.start('job-1', driver);
    const rec = await ex.done();
    expect(order).toEqual(['prepare', 'submit', 'cleanup']);
    expect(rec.phase).toBe('done');
    expect(publicStatusOf(rec)).toBe('submitted');
    expect(rec.submission.outcome).toEqual(accepted);
    assertSerializable(rec);
  });

  test('提交被明确拒绝时对外为 submit-rejected，仍执行清理', async () => {
    let cleaned = false;
    const driver: JobDriver = {
      submit: async () => rejected,
      cleanup: async () => { cleaned = true; },
    };
    const rec = await StagedJobExecutor.start('job-2', driver).done();
    expect(rec.phase).toBe('done');
    expect(publicStatusOf(rec)).toBe('submit-rejected');
    expect(cleaned).toBe(true);
  });
});

describe('取消语义：准备期可取消', () => {
  test('边界前取消（prepare 之前）-> cancelled，submit 未被调用', async () => {
    let submitted = false;
    const driver: JobDriver = { submit: async () => { submitted = true; return accepted; } };
    let ex!: StagedJobExecutor;
    ex = StagedJobExecutor.start('job-3', driver, {
      beforeBoundary: (b: Boundary) => { if (b === 'prepare') ex.cancel(); },
    });
    const rec = await ex.done();
    expect(rec.phase).toBe('cancelled');
    expect(publicStatusOf(rec)).toBe('cancelled');
    expect(submitted).toBe(false);
  });

  test('prepare 进行中取消 -> 不进入提交', async () => {
    const gate = deferred<void>();
    let submitted = false;
    const driver: JobDriver = {
      prepare: () => gate.promise,
      submit: async () => { submitted = true; return accepted; },
    };
    const ex = StagedJobExecutor.start('job-4', driver);
    await tick();
    expect(ex.cancel()).toBe('cancelled');
    gate.resolve(); // prepare 随后才完成，取消仍然生效
    const rec = await ex.done();
    expect(rec.phase).toBe('cancelled');
    expect(submitted).toBe(false);
    expect(rec.submission.started).toBe(false);
  });

  test('submit 边界取消（prepare 已完成、submit 未开始）-> cancelled', async () => {
    let submitted = false;
    const driver: JobDriver = { submit: async () => { submitted = true; return accepted; } };
    let ex!: StagedJobExecutor;
    ex = StagedJobExecutor.start('job-5', driver, {
      beforeBoundary: (b: Boundary) => { if (b === 'submit') ex.cancel(); },
    });
    const rec = await ex.done();
    expect(rec.phase).toBe('cancelled');
    expect(submitted).toBe(false);
  });
});

describe('取消语义：提交一旦开始必须等待明确结果', () => {
  test('submit 在途时取消 -> deferred，等待明确 accepted 后对外 submitted', async () => {
    const gate = deferred<SubmissionOutcome>();
    const { records, onRecord } = recorder();
    const driver: JobDriver = { submit: () => gate.promise };
    const ex = StagedJobExecutor.start('job-6', driver, { onRecord });
    await tick();

    expect(ex.cancel()).toBe('deferred');
    // 提交已在途、结果未知：对外只能是 submit-outcome-unknown
    expect(ex.status()).toBe('submit-outcome-unknown');
    expect(ex.status()).not.toBe('cancelled');
    expect(ex.status()).not.toBe('pending');

    gate.resolve(accepted);
    const rec = await ex.done();
    expect(rec.phase).toBe('done');
    expect(rec.cancelRequested).toBe(true);
    expect(publicStatusOf(rec)).toBe('submitted');
    expect(rec.submission.outcome).toEqual(accepted);

    // 不变量：提交开始后的任何快照都不得报告为"未执行"
    for (const r of records) {
      if (r.submission.started) {
        expect(publicStatusOf(r)).not.toBe('pending');
        expect(publicStatusOf(r)).not.toBe('cancelled');
      }
      assertSerializable(r);
    }
  });

  test('submit 在途时取消，明确 rejected -> submit-rejected 而非 cancelled', async () => {
    const gate = deferred<SubmissionOutcome>();
    const ex = StagedJobExecutor.start('job-7', { submit: () => gate.promise });
    await tick();
    expect(ex.cancel()).toBe('deferred');
    gate.resolve(rejected);
    const rec = await ex.done();
    expect(publicStatusOf(rec)).toBe('submit-rejected');
  });

  test('submit 边界之后取消（提交已开始）-> deferred', async () => {
    const gate = deferred<SubmissionOutcome>();
    let ex!: StagedJobExecutor;
    ex = StagedJobExecutor.start('job-8', { submit: () => gate.promise }, {
      beforeBoundary: async (b: Boundary) => {
        if (b === 'cleanup') expect(ex.cancel()).toBe('deferred');
      },
    });
    await tick();
    gate.resolve(accepted);
    const rec = await ex.done();
    expect(publicStatusOf(rec)).toBe('submitted');
    expect(rec.cancelRequested).toBe(true);
  });
});

describe('清理语义：失败不得丢失提交状态', () => {
  test('cleanup 抛错 -> 提交结果保留，对外仍 submitted', async () => {
    const driver: JobDriver = {
      submit: async () => accepted,
      cleanup: async () => { throw new Error('disk full'); },
    };
    const rec = await StagedJobExecutor.start('job-9', driver).done();
    expect(rec.phase).toBe('done');
    expect(rec.cleanup.lastError).toContain('disk full');
    expect(rec.submission.outcome).toEqual(accepted);
    expect(publicStatusOf(rec)).toBe('submitted');
  });

  test('cleanup 在途时取消 -> 清理继续完成，提交状态保留', async () => {
    const gate = deferred<void>();
    let cancelResult: string | null = null;
    const driver: JobDriver = {
      submit: async () => accepted,
      cleanup: async () => { await gate.promise; },
    };
    const ex = StagedJobExecutor.start('job-10', driver);
    await tick(); await tick();
    // 清理进行中：取消被记录但不打断清理
    cancelResult = ex.cancel();
    expect(cancelResult).toBe('deferred');
    gate.resolve();
    const rec = await ex.done();
    expect(rec.phase).toBe('done');
    expect(publicStatusOf(rec)).toBe('submitted');
  });
});

describe('恢复记录：可序列化恢复', () => {
  test('提交在途时崩溃 -> 从快照恢复，用 token 查询明确结果', async () => {
    const gate = deferred<SubmissionOutcome>();
    const { records, onRecord } = recorder();
    const ex = StagedJobExecutor.start('job-11', { submit: () => gate.promise }, { onRecord });
    await tick();
    // 模拟崩溃：拿到"提交已开始、结果未知"的持久化快照
    const crashSnapshot = records.findLast((r) => r.phase === 'submitting')!;
    expect(crashSnapshot.submission.started).toBe(true);
    expect(crashSnapshot.submission.outcome).toBeNull();
    expect(publicStatusOf(crashSnapshot)).toBe('submit-outcome-unknown');

    // 进程重启：仅从可序列化记录恢复
    const resolver: JobDriver = {
      submit: async () => { throw new Error('must not re-submit'); },
      resolveSubmission: async (_ctx, token) => {
        expect(token).toBe('job-11');
        return accepted;
      },
    };
    const resumed = StagedJobExecutor.resume(crashSnapshot, resolver);
    const rec = await resumed.done();
    expect(rec.phase).toBe('done');
    expect(publicStatusOf(rec)).toBe('submitted');

    gate.resolve(accepted); // 收尾原执行器，避免悬挂
    await ex.done();
  });

  test('准备期取消的意图被持久化 -> 恢复后仍然取消，不会重提交', async () => {
    let submitted = false;
    const gate = deferred<void>();
    const { records, onRecord } = recorder();
    const ex = StagedJobExecutor.start('job-12', {
      prepare: () => gate.promise,
      submit: async () => { submitted = true; return accepted; },
    }, { onRecord });
    await tick();
    ex.cancel();
    // 崩溃发生在 prepare 尚未完成时；最后一份快照带有 cancelRequested
    const crashSnapshot = records.at(-1)!;
    expect(crashSnapshot.cancelRequested).toBe(true);
    expect(crashSnapshot.submission.started).toBe(false);

    const resumed = StagedJobExecutor.resume(crashSnapshot, {
      submit: async () => { submitted = true; return accepted; },
    });
    const rec = await resumed.done();
    expect(rec.phase).toBe('cancelled');
    expect(publicStatusOf(rec)).toBe('cancelled');
    expect(submitted).toBe(false);

    gate.resolve();
    await ex.done();
  });

  test('提交驱动违反契约抛错（无明确结果）-> 保持 submitting，可恢复', async () => {
    const ex = StagedJobExecutor.start('job-13', {
      submit: async () => { throw new Error('connection reset'); },
    });
    const rec = await ex.done();
    // 绝不报告为未执行：结果未知，等待恢复查询
    expect(rec.phase).toBe('submitting');
    expect(publicStatusOf(rec)).toBe('submit-outcome-unknown');

    const resumed = StagedJobExecutor.resume(rec, {
      submit: async () => accepted,
      resolveSubmission: async () => accepted,
    });
    const finalRec = await resumed.done();
    expect(publicStatusOf(finalRec)).toBe('submitted');
  });

  test('不变量扫描：所有场景快照中 started ⇒ 状态不是 pending/cancelled', async () => {
    const { records, onRecord } = recorder();
    const gate = deferred<SubmissionOutcome>();
    let ex!: StagedJobExecutor;
    ex = StagedJobExecutor.start('job-14', {
      prepare: async () => {},
      submit: () => gate.promise,
      cleanup: async () => { throw new Error('cleanup boom'); },
    }, {
      onRecord,
      beforeBoundary: (b: Boundary) => { if (b === 'cleanup') ex.cancel(); },
    });
    await tick();
    ex.cancel();
    gate.resolve(accepted);
    await ex.done();

    expect(records.length).toBeGreaterThan(3);
    for (const r of records) {
      assertSerializable(r);
      const status = publicStatusOf(r);
      if (r.submission.started) {
        expect(['submit-outcome-unknown', 'submitted', 'submit-rejected']).toContain(status);
      }
      // 清理失败不得回退提交状态
      if (r.cleanup.lastError != null && r.submission.outcome?.kind === 'accepted') {
        expect(status).toBe('submitted');
      }
    }
  });
});
