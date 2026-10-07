import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FormStore } from '../src/form.ts';
import type { SubmitResponse, Transport, Values } from '../src/form.ts';

interface Call {
  snapshot: Readonly<Values>;
  resolve: (response: SubmitResponse) => void;
  reject: (error: unknown) => void;
}

/** A transport whose responses are triggered manually by the test. */
function controllableTransport() {
  const calls: Call[] = [];
  const transport: Transport = (snapshot) =>
    new Promise<SubmitResponse>((resolve, reject) => {
      calls.push({ snapshot, resolve, reject });
    });
  return { transport, calls };
}

test('submit freezes a snapshot of the values at submit time', () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1', b: '2' });

  const record = form.submit();
  form.setValue('a', 'edited-after-submit');
  form.deleteField('b');

  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].snapshot, { a: '1', b: '2' });
  assert.ok(Object.isFrozen(calls[0].snapshot));
  assert.equal(record.snapshot, calls[0].snapshot);
  assert.equal(record.status, 'pending');
});

test('a success response confirms only the submitted version; edits during pending stay dirty', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1', b: '2' });

  const record = form.submit();
  form.setValue('a', '1-edited');

  calls[0].resolve({ ok: true });
  await record.settled;

  assert.equal(record.status, 'confirmed');
  // Untouched field is confirmed clean.
  assert.equal(form.isDirty('b'), false);
  assert.equal(form.confirmedValue('b'), '2');
  // Field edited during the flight stays dirty against the confirmed version.
  assert.equal(form.isDirty('a'), true);
  assert.equal(form.confirmedValue('a'), '1');
  assert.equal(form.getValue('a'), '1-edited');
  assert.equal(form.isDirty(), true);
});

test('server field errors apply only to fields still equal to the submitted value', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1', b: '2', c: '3' });

  const record = form.submit();
  form.setValue('b', '2-edited'); // edited after submit
  form.deleteField('c'); // deleted after submit

  calls[0].resolve({
    ok: false,
    message: 'validation failed',
    fieldErrors: { a: 'bad a', b: 'bad b', c: 'bad c' },
  });
  await record.settled;

  assert.equal(record.status, 'failed');
  assert.equal(record.message, 'validation failed');
  // Applied: field still holds the submitted value.
  assert.equal(form.getError('a'), 'bad a');
  assert.deepEqual(record.appliedErrors, { a: 'bad a' });
  // Kept only as submission record: edited or deleted since the snapshot.
  assert.equal(form.getError('b'), undefined);
  assert.equal(form.getError('c'), undefined);
  assert.deepEqual(record.unappliedErrors, { b: 'bad b', c: 'bad c' });
  // Local edits are untouched by the failure.
  assert.equal(form.getValue('b'), '2-edited');
  assert.equal(form.isDirty('b'), true);
});

test('duplicate submit while pending returns the in-flight submission', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1' });

  const first = form.submit();
  form.setValue('a', '2');
  const second = form.submit();

  assert.equal(second, first);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].snapshot, { a: '1' });

  calls[0].resolve({ ok: true });
  await first.settled;

  // After the flight settles, a new submit sends a new version.
  const third = form.submit();
  assert.notEqual(third, first);
  assert.equal(third.id, first.id + 1);
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1].snapshot, { a: '2' });
  calls[1].resolve({ ok: true });
  await third.settled;
  assert.equal(form.isDirty(), false);
});

test('cancel detaches the submission and a late response confirms nothing', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1' });

  const record = form.submit();
  assert.equal(form.cancel(), true);
  assert.equal(form.cancel(), false); // nothing pending anymore
  assert.equal(record.status, 'cancelled');
  assert.equal(form.pending, null);

  // The late response arrives after cancellation.
  calls[0].resolve({ ok: true });
  await record.settled;

  assert.equal(record.status, 'cancelled');
  assert.equal(form.isDirty('a'), false); // value never changed locally
  form.setValue('a', 'local-edit');
  assert.equal(form.confirmedValue('a'), '1'); // not overwritten by stale ok
  assert.equal(form.isDirty('a'), true);

  // Cancelling a failed response works the same way.
  const second = form.submit();
  form.cancel();
  calls[1].resolve({ ok: false, fieldErrors: { a: 'stale error' } });
  await second.settled;
  assert.equal(second.status, 'cancelled');
  assert.equal(form.getError('a'), undefined);
  assert.deepEqual(second.appliedErrors, {});
  assert.deepEqual(second.unappliedErrors, {});
});

test('a field deleted during pending is not confirmed and its error stays on the record', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { keep: 'k', drop: 'd' });

  const okRecord = form.submit();
  form.deleteField('drop');
  calls[0].resolve({ ok: true });
  await okRecord.settled;

  assert.equal(okRecord.status, 'confirmed');
  assert.equal(form.hasField('drop'), false);
  assert.equal(form.confirmedValue('drop'), 'd'); // old confirmed value untouched
  assert.equal(form.isDirty('drop'), true); // deletion itself is a local change
  assert.equal(form.isDirty('keep'), false);

  const failRecord = form.submit();
  calls[1].resolve({ ok: false, fieldErrors: { drop: 'cannot be blank' } });
  await failRecord.settled;

  assert.equal(form.getError('drop'), undefined);
  assert.deepEqual(failRecord.appliedErrors, {});
  assert.deepEqual(failRecord.unappliedErrors, { drop: 'cannot be blank' });
});

test('transport rejection records a failed submission without touching fields', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1' });

  const record = form.submit();
  calls[0].reject(new Error('network down'));
  await record.settled;

  assert.equal(record.status, 'failed');
  assert.equal(record.message, 'network down');
  assert.equal(form.getError('a'), undefined);
  assert.equal(form.isDirty(), false);
  assert.equal(form.pending, null);
});

test('submission history keeps one record per submit', async () => {
  const { transport, calls } = controllableTransport();
  const form = new FormStore(transport, { a: '1' });

  const first = form.submit();
  calls[0].resolve({ ok: true });
  await first.settled;
  const second = form.submit();
  form.cancel();
  await second.settled;
  const third = form.submit();
  assert.equal(calls.length, 3); // the cancelled submission also used a call
  calls[2].resolve({ ok: false, message: 'nope' });
  await third.settled;

  assert.deepEqual(
    form.submissions.map((s) => s.status),
    ['confirmed', 'cancelled', 'failed'],
  );
  assert.equal(form.submissions[2].message, 'nope');
});
