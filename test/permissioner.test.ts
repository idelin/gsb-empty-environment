import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { PausablePermissioner } from '../src/permissioner.ts';
import type { ClipboardPermissionRequest } from '../src/types.ts';

function request(id: string): ClipboardPermissionRequest {
  return {
    requestId: id,
    mode: 'write',
    snapshot: { tabId: 'tab-a', documentGeneration: 1, focusGeneration: 1 },
    activationTokenId: 'act-1',
  };
}

test('暂停时请求排队，不产生任何回调', () => {
  const permissioner = new PausablePermissioner(new ManualClock());
  let settled = false;
  permissioner.requestPermission(request('req-1')).then(
    () => {
      settled = true;
    },
    () => {
      settled = true;
    },
  );
  assert.equal(permissioner.pendingCount(), 1);
  assert.deepEqual(permissioner.pendingRequestIds(), ['req-1']);
  assert.equal(settled, false);
});

test('逐条批准返回彼此独立的单次授权', async () => {
  const permissioner = new PausablePermissioner(new ManualClock());
  const first = permissioner.requestPermission(request('req-1'));
  const second = permissioner.requestPermission(request('req-2'));

  const grantOne = permissioner.grantNext();
  const grantTwo = permissioner.grantRequest('req-2');

  assert.equal(grantOne.requestId, 'req-1');
  assert.equal(grantTwo.requestId, 'req-2');
  assert.notEqual(grantOne.grantId, grantTwo.grantId);
  assert.deepEqual(
    permissioner.issuedGrants.map((grant) => grant.requestId),
    ['req-1', 'req-2'],
  );

  assert.equal(permissioner.isConsumed(grantOne.grantId), false);
  permissioner.markConsumed(grantOne);
  assert.equal(permissioner.isConsumed(grantOne.grantId), true);
  assert.equal(permissioner.isConsumed(grantTwo.grantId), false);

  await Promise.all([first, second]);
});

test('拒绝与 flush 只影响对应请求，不消费其他授权', async () => {
  const permissioner = new PausablePermissioner(new ManualClock());
  const denied = permissioner.requestPermission(request('req-bad'));
  const allowed = permissioner.requestPermission(request('req-good'));

  permissioner.denyRequest('req-bad');
  const grant = permissioner.grantRequest('req-good');

  await assert.rejects(denied, /denied/);
  await assert.doesNotReject(allowed);
  assert.equal(permissioner.isConsumed(grant.grantId), false);
  assert.deepEqual(
    permissioner.issuedGrants.map((item) => item.requestId),
    ['req-good'],
  );
});

test('flush 按入队顺序批量回调', async () => {
  const permissioner = new PausablePermissioner(new ManualClock());
  const promises = [
    permissioner.requestPermission(request('r1')),
    permissioner.requestPermission(request('r2')),
    permissioner.requestPermission(request('r3')),
  ];
  const grants = permissioner.flush(true);
  assert.equal(grants.length, 3);
  assert.deepEqual(grants.map((grant) => grant.requestId), ['r1', 'r2', 'r3']);
  assert.equal(permissioner.pendingCount(), 0);
  await Promise.all(promises);
});

test('作废后的授权同样被 isConsumed 识别', () => {
  const permissioner = new PausablePermissioner(new ManualClock());
  const pending = permissioner.requestPermission(request('req-x'));
  const issued = permissioner.grantNext();
  permissioner.invalidateGrant(issued, 'test');
  assert.equal(permissioner.isConsumed(issued.grantId), true);
  assert.deepEqual(permissioner.invalidatedGrants, [
    { grantId: issued.grantId, reason: 'test' },
  ]);
  return pending;
});
