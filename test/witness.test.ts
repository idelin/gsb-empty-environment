import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ManualClock } from '../src/clock.ts';
import { Witness } from '../src/witness.ts';
import { LeaseHeldError, StaleLeaseCredentialError } from '../src/errors.ts';

const TTL = 10_000;

test('法官向首个申请者颁发租约，epoch 从 1 开始', () => {
  const w = new Witness(new ManualClock());
  const g = w.requestLease('dc1', TTL);
  assert.equal(g.holderId, 'dc1');
  assert.equal(g.epoch, 1);
  assert.equal(g.expiresAt, TTL);
});

test('租约活跃期间拒绝向其他机房颁发（防双主）', () => {
  const clock = new ManualClock();
  const w = new Witness(clock);
  w.requestLease('dc1', TTL);
  clock.advance(TTL - 1);
  assert.throws(() => w.requestLease('dc2', TTL), LeaseHeldError);
});

test('租约过期后法官可向备机房颁发接管租约，epoch 单调递增', () => {
  const clock = new ManualClock();
  const w = new Witness(clock);
  const g1 = w.requestLease('dc1', TTL);
  clock.advance(TTL); // 到期
  const g2 = w.requestLease('dc2', TTL);
  assert.equal(g2.holderId, 'dc2');
  assert.ok(g2.epoch > g1.epoch, 'fencing token 必须递增');
});

test('续租延长过期时间；旧凭证（错误 epoch/持有者）续租被拒绝', () => {
  const clock = new ManualClock();
  const w = new Witness(clock);
  const g = w.requestLease('dc1', TTL);
  clock.advance(4_000);
  const r = w.renewLease('dc1', g.epoch, TTL);
  assert.equal(r.expiresAt, 14_000);
  assert.throws(() => w.renewLease('dc1', 999, TTL), StaleLeaseCredentialError);
  assert.throws(() => w.renewLease('dc2', g.epoch, TTL), StaleLeaseCredentialError);
});

test('接管发生后旧主凭证失效：validate=false 且续租抛 StaleLeaseCredentialError', () => {
  const clock = new ManualClock();
  const w = new Witness(clock);
  const g1 = w.requestLease('dc1', TTL);
  clock.advance(TTL);
  w.requestLease('dc2', TTL);
  assert.equal(w.validate('dc1', g1.epoch), false);
  assert.throws(() => w.renewLease('dc1', g1.epoch, TTL), StaleLeaseCredentialError);
});
