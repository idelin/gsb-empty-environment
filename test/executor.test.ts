import assert from 'assert';
import { BOUNDARIES, Boundary, StagedJobExecutor } from '../src/executor';
import { PublicStatus, RecoveryRecord } from '../src/phases';
import { deserializeRecord, serializeRecord, toPublicStatus } from '../src/record';
import { canTransition, assertTransition } from '../src/stateMachine';
import { InvalidTransitionError } from '../src/errors';
import { FakeBackend } from './fakeBackend';

/**
 * THE invariant under test: whatever happens (cancellation at any async
 * boundary, cleanup failure, crash+recovery), the externally visible
 * status must never report a submitted job as not executed.
 */
function assertNeverLiesAboutSubmission(record: RecoveryRecord, status: PublicStatus): void {
  if (record.submission.kind === 'accepted') {
    assert.strictEqual(status.executed, true, 'accepted submission must report executed=true');
    assert.strictEqual(status.receipt, record.submission.receipt);
    assert.notStrictEqual(status.phase, 'cancelled', 'submitted job must not look cancelled');
  }
  if (record.submission.kind === 'in-doubt') {
    assert.strictEqual(
      status.executed,
      'unknown',
      'in-doubt submission must report executed=unknown, never false',
    );
  }
  if (status.executed === false) {
    assert.ok(
      record.submission.kind === 'not-started' || record.submission.kind === 'rejected',
      `executed=false is only legal pre-commit or after definitive rejection, got ${record.submission.kind}`,
    );
  }
}

let failures = 0;
async function test(name: string, fn: () => void | Promise<void>): Promise<void> {
  try {
    await fn();
    console.log(`ok   - ${name}`);
  } catch (err) {
    failures += 1;
    console.error(`FAIL - ${name}`);
    console.error(err);
  }
}

async function main(): Promise<void> {
  await test('happy path: prepare -> submit -> cleanup -> completed', async () => {
    const backend = new FakeBackend();
    const exec = new StagedJobExecutor({ jobId: 'job-happy', backend });
    const status = await exec.run();
    assert.strictEqual(status.phase, 'completed');
    assert.strictEqual(status.executed, true);
    assert.strictEqual(status.receipt, 'receipt-001');
    assert.deepStrictEqual(
      [backend.prepareCalls, backend.submitCalls, backend.cleanupCalls],
      [1, 1, 1],
    );
    assertNeverLiesAboutSubmission(exec.snapshot(), status);
  });

  // Cancellation injected at EVERY async boundary, one run per boundary.
  for (const boundary of BOUNDARIES) {
    await test(`cancel at boundary "${boundary}" keeps external state honest`, async () => {
      const backend = new FakeBackend();
      const exec = new StagedJobExecutor({
        jobId: `job-cancel-${boundary}`,
        backend,
        onBoundary: (name: Boundary) => {
          if (name === boundary) exec.cancel();
        },
      });
      const status = await exec.run();
      const record = exec.snapshot();

      assert.strictEqual(record.cancelRequested, true);
      assertNeverLiesAboutSubmission(record, status);

      const preCommit = boundary === 'prepare' || boundary === 'after-prepare';
      if (preCommit) {
        // Cancelled before the commit point: submit must never run and
        // "not executed" is the truthful answer.
        assert.strictEqual(status.phase, 'cancelled');
        assert.strictEqual(status.executed, false);
        assert.strictEqual(backend.submitCalls, 0);
      } else {
        // Cancelled at/after the commit point: the executor must have
        // waited for the definitive result and must report it.
        assert.strictEqual(backend.submitCalls, 1);
        assert.strictEqual(status.phase, 'completed');
        assert.strictEqual(status.executed, true);
        assert.strictEqual(status.receipt, 'receipt-001');
        assert.strictEqual(backend.cleanupCalls, 1);
      }
    });
  }

  await test('cancel mid-prepare (token cancelled inside backend) aborts before submit', async () => {
    const backend = new FakeBackend();
    const exec = new StagedJobExecutor({ jobId: 'job-mid-prepare', backend });
    backend.duringPrepare = () => exec.cancel();
    const status = await exec.run();
    assert.strictEqual(status.phase, 'cancelled');
    assert.strictEqual(status.executed, false);
    assert.strictEqual(backend.submitCalls, 0);
    assertNeverLiesAboutSubmission(exec.snapshot(), status);
  });

  await test('cancel mid-submit still waits for the definitive receipt', async () => {
    const backend = new FakeBackend();
    const exec = new StagedJobExecutor({ jobId: 'job-mid-submit', backend });
    let sawSubmitInFlight = false;
    const status = await (async () => {
      const running = exec.run();
      // Wait until the submit call is definitely in flight, then cancel.
      while (backend.submitCalls === 0) await Promise.resolve();
      sawSubmitInFlight = true;
      exec.cancel();
      return running;
    })();
    assert.ok(sawSubmitInFlight);
    assert.strictEqual(status.phase, 'completed');
    assert.strictEqual(status.executed, true);
    assert.strictEqual(status.cancelRequested, true);
    assert.strictEqual(status.receipt, 'receipt-001');
    assertNeverLiesAboutSubmission(exec.snapshot(), status);
  });

  await test('cleanup failure preserves the accepted submission state', async () => {
    const backend = new FakeBackend();
    backend.cleanupError = 'cleanup boom';
    const exec = new StagedJobExecutor({ jobId: 'job-cleanup-fail', backend });
    const status = await exec.run();
    assert.strictEqual(status.phase, 'completed');
    assert.strictEqual(status.executed, true, 'submission state survives cleanup failure');
    assert.strictEqual(status.receipt, 'receipt-001');
    assert.strictEqual(status.cleanupError, 'cleanup boom');
    const record = exec.snapshot();
    assert.strictEqual(record.submission.kind, 'accepted');
    assert.strictEqual(record.cleanup.attempted, true);
    assertNeverLiesAboutSubmission(record, status);
  });

  await test('definitive rejection reports executed=false and still cleans up', async () => {
    const backend = new FakeBackend();
    backend.submitBehavior = 'reject';
    const exec = new StagedJobExecutor({ jobId: 'job-rejected', backend });
    const status = await exec.run();
    assert.strictEqual(status.phase, 'completed');
    assert.strictEqual(status.executed, false);
    assert.strictEqual(exec.snapshot().submission.kind, 'rejected');
    assert.strictEqual(backend.cleanupCalls, 1);
    assertNeverLiesAboutSubmission(exec.snapshot(), status);
  });

  await test('inconclusive submit error parks in-doubt, never reports not-executed', async () => {
    const backend = new FakeBackend();
    backend.submitBehavior = 'inconclusive';
    const exec = new StagedJobExecutor({ jobId: 'job-in-doubt', backend });
    const status = await exec.run();
    assert.strictEqual(status.phase, 'in-doubt');
    assert.strictEqual(status.executed, 'unknown');
    assertNeverLiesAboutSubmission(exec.snapshot(), status);

    // Recovery: reconcile discovers the job WAS accepted remotely.
    const restored = StagedJobExecutor.restore(exec.serialize(), { backend: new FakeBackend() });
    const recovered = await restored.resume();
    assert.strictEqual(recovered.phase, 'completed');
    assert.strictEqual(recovered.executed, true);
    assert.strictEqual(recovered.receipt, 'reconciled-receipt');
    assertNeverLiesAboutSubmission(restored.snapshot(), recovered);
  });

  await test('crash mid-submit: recovery record says in-doubt, resume reconciles', async () => {
    const backend = new FakeBackend();
    let crashedImage = '';
    const exec = new StagedJobExecutor({
      jobId: 'job-crash',
      backend,
      onBoundary: (name) => {
        // "Power loss" right as submit starts: capture the persisted record.
        if (name === 'submit') crashedImage = exec.serialize();
      },
    });
    const running = exec.run();
    while (crashedImage === '') await Promise.resolve();

    // The crashed image must not claim "not executed".
    const crashedRecord = deserializeRecord(crashedImage);
    assert.strictEqual(crashedRecord.phase, 'submitting');
    assert.strictEqual(crashedRecord.submission.kind, 'in-doubt');
    const crashedStatus = toPublicStatus(crashedRecord);
    assert.strictEqual(crashedStatus.executed, 'unknown');
    assertNeverLiesAboutSubmission(crashedRecord, crashedStatus);

    // A fresh process restores from the image and finishes the job.
    const recoveryBackend = new FakeBackend();
    const restored = StagedJobExecutor.restore(crashedImage, { backend: recoveryBackend });
    const recovered = await restored.resume();
    assert.strictEqual(recoveryBackend.reconcileCalls, 1);
    assert.strictEqual(recovered.phase, 'completed');
    assert.strictEqual(recovered.executed, true);
    assertNeverLiesAboutSubmission(restored.snapshot(), recovered);

    await running; // let the "crashed" process finish quietly
  });

  await test('cancel at reconcile boundary during resume still waits for outcome', async () => {
    const backend = new FakeBackend();
    backend.submitBehavior = 'inconclusive';
    const exec = new StagedJobExecutor({ jobId: 'job-reconcile-cancel', backend });
    await exec.run();
    assert.strictEqual(exec.phase, 'in-doubt');

    const restored = StagedJobExecutor.restore(exec.serialize(), {
      backend: new FakeBackend(),
      onBoundary: (name) => {
        if (name === 'reconcile') restored.cancel();
      },
    });
    const status = await restored.resume();
    assert.strictEqual(status.executed, true);
    assert.strictEqual(status.phase, 'completed');
    assert.strictEqual(status.cancelRequested, true);
    assertNeverLiesAboutSubmission(restored.snapshot(), status);
  });

  await test('resume from cleaning phase re-runs cleanup only', async () => {
    const backend = new FakeBackend();
    let imageAtCleanup = '';
    const exec = new StagedJobExecutor({
      jobId: 'job-crash-cleanup',
      backend,
      onBoundary: (name) => {
        if (name === 'cleanup') imageAtCleanup = exec.serialize();
      },
    });
    const running = exec.run();
    while (imageAtCleanup === '') await Promise.resolve();

    const recoveryBackend = new FakeBackend();
    const restored = StagedJobExecutor.restore(imageAtCleanup, { backend: recoveryBackend });
    const status = await restored.resume();
    assert.strictEqual(recoveryBackend.submitCalls, 0, 'must not resubmit');
    assert.strictEqual(recoveryBackend.cleanupCalls, 1);
    assert.strictEqual(status.executed, true);
    assert.strictEqual(status.phase, 'completed');
    await running;
  });

  await test('recovery record survives JSON serialization round-trip', async () => {
    const backend = new FakeBackend();
    backend.cleanupError = 'disk full';
    const exec = new StagedJobExecutor({ jobId: 'job-roundtrip', backend });
    await exec.run();
    const record = exec.snapshot();
    const revived = deserializeRecord(serializeRecord(record));
    assert.deepStrictEqual(revived, record);
    // Status derived from the revived record is identical.
    assert.deepStrictEqual(toPublicStatus(revived), toPublicStatus(record));
  });

  await test('state machine rejects illegal transitions', () => {
    assert.ok(canTransition('preparing', 'cancelled'));
    assert.ok(canTransition('preparing', 'submitting'));
    assert.ok(canTransition('submitting', 'in-doubt'));
    assert.ok(!canTransition('submitting', 'cancelled'), 'no cancel after commit point');
    assert.ok(!canTransition('completed', 'preparing'));
    assert.ok(!canTransition('cancelled', 'submitting'));
    assert.throws(() => assertTransition('submitting', 'cancelled'), InvalidTransitionError);
    assert.throws(() => assertTransition('idle', 'cleaning'), InvalidTransitionError);
  });

  await test('prepare failure lands in failed with executed=false', async () => {
    const backend = new FakeBackend();
    backend.duringPrepare = () => {
      throw new Error('out of disk');
    };
    const exec = new StagedJobExecutor({ jobId: 'job-prepare-fail', backend });
    const status = await exec.run();
    assert.strictEqual(status.phase, 'failed');
    assert.strictEqual(status.executed, false);
    assert.strictEqual(status.failure, 'out of disk');
    assert.strictEqual(backend.submitCalls, 0);
    assertNeverLiesAboutSubmission(exec.snapshot(), status);
  });

  console.log(failures === 0 ? '\nall tests passed' : `\n${failures} test(s) failed`);
  if (failures > 0) process.exit(1);
}

void main();
