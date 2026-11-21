import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SessionService, SessionError } from '../src/session-rotation.ts';

const expectError = (fn: () => unknown, code: string): SessionError => {
  try {
    fn();
  } catch (err) {
    assert.ok(err instanceof SessionError, `expected SessionError, got ${err}`);
    assert.equal(err.code, code);
    return err;
  }
  assert.fail(`expected SessionError ${code}`);
};

test('rotation issues a fresh id and retires the old one', () => {
  const svc = new SessionService();
  const anon = svc.createSession({ locale: 'zh-CN' });

  const authed = svc.rotate(anon.id, { context: { userId: 'u-1' } });

  assert.notEqual(authed.id, anon.id);
  assert.equal(anon.status, 'rotated');
  assert.equal(anon.rotatedTo, authed.id);
  assert.equal(svc.resolve(authed.id).status, 'active');
});

test('rotation keeps persistent context but drops the permission cache', () => {
  const svc = new SessionService();
  const anon = svc.createSession({ locale: 'zh-CN', cartId: 'cart-9' });
  svc.cachePermission(anon.id, 'roles', ['admin']);

  const authed = svc.rotate(anon.id, { context: { userId: 'u-1' } });

  assert.deepEqual(authed.persistentContext, {
    locale: 'zh-CN',
    cartId: 'cart-9',
    userId: 'u-1',
  });
  assert.equal(authed.permissionCache.size, 0);
  assert.equal(svc.getCachedPermission(authed.id, 'roles'), undefined);
});

test('stale tab next request is rejected with a recognizable status', () => {
  const svc = new SessionService();
  const anon = svc.createSession();
  const authed = svc.rotate(anon.id);

  const err = expectError(() => svc.resolve(anon.id), 'SESSION_ROTATED');
  assert.equal(err.httpStatus, 409);
  assert.equal(err.rotatedTo, authed.id);

  // The fresh tab keeps working.
  assert.equal(svc.resolve(authed.id).id, authed.id);
});

test('parallel tabs: only one rotation wins, loser gets the successor id', async () => {
  const svc = new SessionService();
  const anon = svc.createSession();

  const results = await Promise.allSettled([
    Promise.resolve().then(() => svc.rotate(anon.id)),
    Promise.resolve().then(() => svc.rotate(anon.id)),
    Promise.resolve().then(() => svc.rotate(anon.id)),
  ]);

  const winners = results.filter((r) => r.status === 'fulfilled');
  const losers = results.filter((r) => r.status === 'rejected');

  assert.equal(winners.length, 1);
  assert.equal(losers.length, 2);

  const winnerId = (winners[0] as PromiseFulfilledResult<{ id: string }>).value.id;
  for (const loser of losers) {
    const reason = (loser as PromiseRejectedResult).reason;
    assert.ok(reason instanceof SessionError);
    assert.equal(reason.code, 'SESSION_ROTATED');
    assert.equal(reason.rotatedTo, winnerId);
  }
});

test('parallel requests on the old id are all rejected after rotation', async () => {
  const svc = new SessionService();
  const anon = svc.createSession();
  svc.rotate(anon.id);

  const outcomes = await Promise.all(
    Array.from({ length: 8 }, () =>
      Promise.resolve().then(() => {
        try {
          svc.resolve(anon.id);
          return 'accepted';
        } catch (err) {
          assert.ok(err instanceof SessionError);
          return err.code;
        }
      }),
    ),
  );

  assert.deepEqual(outcomes, Array(8).fill('SESSION_ROTATED'));
});

test('anti-replay tokens consumed before rotation stay consumed after it', () => {
  const svc = new SessionService();
  const anon = svc.createSession();
  svc.consumeReplayToken(anon.id, 'nonce-A');

  const authed = svc.rotate(anon.id);

  // Ledger migrated: the pre-rotation token is still known to the new session.
  assert.ok(svc.hasConsumedReplayToken(authed.id, 'nonce-A'));
  expectError(() => svc.consumeReplayToken(authed.id, 'nonce-A'), 'REPLAY_TOKEN_REUSED');

  // Fresh tokens still work on the rotated session.
  svc.consumeReplayToken(authed.id, 'nonce-B');
});

test('token reuse is rejected within and across sessions', () => {
  const svc = new SessionService();
  const first = svc.createSession();
  svc.consumeReplayToken(first.id, 'nonce-X');
  expectError(() => svc.consumeReplayToken(first.id, 'nonce-X'), 'REPLAY_TOKEN_REUSED');

  // Reuse attempt via the stale id surfaces the rotation status, not acceptance.
  svc.rotate(first.id);
  expectError(() => svc.consumeReplayToken(first.id, 'nonce-X'), 'SESSION_ROTATED');
});

test('unknown session ids are rejected distinctly from rotated ones', () => {
  const svc = new SessionService();
  const err = expectError(() => svc.resolve('does-not-exist'), 'SESSION_NOT_FOUND');
  assert.equal(err.httpStatus, 401);
});
