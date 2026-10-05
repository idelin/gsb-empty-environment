import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Center } from '../src/center.ts';
import { EdgeNode } from '../src/node.ts';
import { Ledger } from '../src/ledger.ts';
import { Sim } from '../src/sim.ts';
import { auditDir } from '../src/audit.ts';
import { demoScenario } from '../src/scenario.ts';
import type { Grant, ReturnConfirmMsg, ReturnMsg } from '../src/types.ts';

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'quota-'));
}

function makeCenter(dir: string, windowLength = 10, budget = 100): Center {
  return new Center(windowLength, budget, new Ledger(dir, 'center'));
}

function makeNode(dir: string, id = 'n1', windowLength = 10): EdgeNode {
  return new EdgeNode(id, windowLength, new Ledger(dir, `node-${id}`));
}

test('center never issues beyond window budget; new window resets', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  assert.equal(c.issue('n1', 60, 0).ok, true);
  assert.equal(c.issue('n2', 40, 0).ok, true);
  const r = c.issue('n1', 1, 0);
  assert.equal(r.ok, false);
  assert.equal(r.reason, 'window-budget-exceeded');
  assert.equal(c.issue('n1', 100, 10).ok, true); // 窗口 1 全新额度
});

test('grant carries windowId, nodeId, grantId, amount, expiry tick', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const g = c.issue('n1', 50, 3).grant as Grant;
  assert.deepEqual(
    { windowId: g.windowId, nodeId: g.nodeId, grantId: g.grantId, amount: g.amount, expiresAtTick: g.expiresAtTick },
    { windowId: 0, nodeId: 'n1', grantId: 'G1', amount: 50, expiresAtTick: 9 },
  );
});

test('spend is idempotent by requestId and counted once', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  const g = c.issue('n1', 100, 0).grant as Grant;
  n.onGrant(g, 1);
  const a = n.spend('r1', 30, 2);
  const b = n.spend('r1', 30, 3);
  assert.equal(a.status, 'accepted');
  assert.equal(b.status, 'accepted');
  assert.equal(b.status === 'accepted' && b.dedup, true);
  assert.equal(n.grantState('G1')?.spent, 30);
});

test('same requestId with different cost is a conflict and changes nothing', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  n.onGrant(c.issue('n1', 100, 0).grant as Grant, 1);
  n.spend('r1', 30, 2);
  const r = n.spend('r1', 40, 3);
  assert.equal(r.status, 'conflict');
  assert.equal(r.status === 'conflict' && r.originalCost, 30);
  assert.equal(n.grantState('G1')?.spent, 30);
  const again = n.spend('r1', 30, 4);
  assert.equal(again.status, 'accepted');
  assert.equal(again.status === 'accepted' && again.dedup, true);
});

test('deadline tick is inclusive; expiry is enforced after it', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  n.onGrant(c.issue('n1', 100, 0).grant as Grant, 0); // exp = 9
  assert.equal(n.spend('r1', 100, 9).status, 'accepted'); // 截止 tick 可消费
  const r = n.spend('r2', 1, 10);
  assert.equal(r.status, 'rejected');
  assert.equal(r.status === 'rejected' && r.reason, 'no-active-grants');
});

test('expired quota cannot be used across windows', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  n.onGrant(c.issue('n1', 100, 0).grant as Grant, 0);
  const r = n.spend('r1', 50, 10); // 窗口 1，旧授予已过期
  assert.equal(r.status, 'rejected');
  assert.equal(r.status === 'rejected' && r.reason, 'no-active-grants');
});

test('late grant cannot revive expired budget', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  const g = c.issue('n1', 100, 0).grant as Grant; // exp = 9
  n.onGrant(g, 15); // 迟到授予
  const r = n.spend('r1', 10, 15);
  assert.equal(r.status, 'rejected');
  assert.equal(r.status === 'rejected' && r.reason, 'no-active-grants');
});

test('reissue only after center confirms return; timeout never frees quota', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  const g = c.issue('n1', 100, 0).grant as Grant;
  n.onGrant(g, 0);
  n.spend('r1', 30, 1);
  const ret = n.returnGrant('G1', 2) as ReturnMsg; // unspent = 70
  assert.equal(ret.unspent, 70);
  // 归还消息在途中丢失：推进很多 tick，中心可用量不变（超时不能推断未用）
  assert.equal(c.available(0), 0);
  assert.equal(c.issue('n2', 1, 5).ok, false);
  assert.equal(c.available(0), 0);
  // 归还到达并确认后才能重发
  c.onReturn(ret, 6);
  assert.equal(c.available(0), 70);
  const confirm = c.drainOutbox().find((m) => m.kind === 'return-confirm') as ReturnConfirmMsg;
  n.onReturnConfirm(confirm, 7);
  assert.equal(n.grantState('G1')?.confirmed, true);
  const re = c.issue('n2', 70, 8);
  assert.equal(re.ok, true);
  assert.equal(re.grant?.expiresAtTick, 9); // 重发仍限本窗口
});

test('duplicate return is idempotent at center (counted once)', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n = makeNode(dir);
  const g = c.issue('n1', 100, 0).grant as Grant;
  n.onGrant(g, 0);
  n.spend('r1', 25, 1);
  const ret = n.returnGrant('G1', 2) as ReturnMsg;
  c.onReturn(ret, 3);
  c.onReturn(ret, 4); // 重复投递
  c.onReturn(ret, 5);
  assert.equal(c.available(0), 75);
  const confirms = c.drainOutbox().filter((m) => m.kind === 'return-confirm');
  assert.equal(confirms.length, 3); // 每次都重新确认，但金额一致
  assert.ok(confirms.every((m) => m.kind === 'return-confirm' && m.unspent === 75));
});

test('return vs spend race is decided by node-local atomic state', () => {
  // 先归还后消费：消费被拒
  {
    const dir = tmpdir();
    const c = makeCenter(dir);
    const n = makeNode(dir);
    n.onGrant(c.issue('n1', 100, 0).grant as Grant, 0);
    n.returnGrant('G1', 1);
    const r = n.spend('r1', 10, 1);
    assert.equal(r.status, 'rejected');
    assert.equal(r.status === 'rejected' && r.reason, 'no-active-grants');
  }
  // 先消费后归还：归还金额扣除已消费
  {
    const dir = tmpdir();
    const c = makeCenter(dir);
    const n = makeNode(dir);
    n.onGrant(c.issue('n1', 100, 0).grant as Grant, 0);
    n.spend('r1', 40, 1);
    const ret = n.returnGrant('G1', 1) as ReturnMsg;
    assert.equal(ret.unspent, 60);
  }
});

test('node restart replays ledger: grants, spends and idempotency survive', () => {
  const dir = tmpdir();
  const c = makeCenter(dir);
  const n1 = makeNode(dir);
  n1.onGrant(c.issue('n1', 100, 0).grant as Grant, 0);
  n1.spend('r1', 30, 1);
  const n2 = makeNode(dir); // 重启：从账本重放
  assert.equal(n2.grantState('G1')?.spent, 30);
  const dup = n2.spend('r1', 30, 2);
  assert.equal(dup.status, 'accepted');
  assert.equal(dup.status === 'accepted' && dup.dedup, true);
  n2.spend('r2', 70, 2);
  assert.equal(n2.grantState('G1')?.spent, 100);
  const r = n2.spend('r3', 1, 3);
  assert.equal(r.status, 'rejected');
});

test('center restart replays ledger: budget accounting and grant sequence survive', () => {
  const dir = tmpdir();
  const c1 = makeCenter(dir);
  c1.issue('n1', 60, 0);
  c1.issue('n2', 40, 0);
  const c2 = makeCenter(dir);
  assert.equal(c2.available(0), 0);
  const g = c2.issue('n1', 50, 10).grant as Grant;
  assert.equal(g.grantId, 'G3'); // 序列不与重启前冲突
});

test('auditor verifies windows, dedups ids, reports idle and rejections', () => {
  const dir = tmpdir();
  const scenario = {
    ticks: 30,
    windowLength: 10,
    windowBudget: 100,
    nodes: ['n1', 'n2'],
    ops: [
      { tick: 0, op: 'issue', node: 'n1', amount: 80 },
      { tick: 0, op: 'issue', node: 'n2', amount: 20 },
      { tick: 2, op: 'spend', node: 'n1', requestId: 'a', cost: 50 },
      { tick: 3, op: 'spend', node: 'n1', requestId: 'a', cost: 50 }, // 幂等，不计两次
      { tick: 4, op: 'spend', node: 'n2', requestId: 'b', cost: 25 }, // 超出 -> 拒绝
      { tick: 12, op: 'spend', node: 'n1', requestId: 'c', cost: 10 }, // 跨窗 -> 拒绝
    ],
  };
  const sim = new Sim(scenario, dir);
  sim.run();
  const report = auditDir(dir, 10, 100);
  assert.equal(report.ok, true);
  assert.equal(report.acceptedUnique, 1);
  const w0 = report.windows.find((w) => w.windowId === 0);
  assert.equal(w0?.consumed, 50);
  assert.equal(w0?.idle, 50);
  assert.equal(report.rejections['insufficient-quota'], 1);
  assert.equal(report.rejections['no-active-grants'], 1);
});

test('auditor flags overspend and duplicate ids in tampered ledgers', () => {
  const dir = tmpdir();
  fs.writeFileSync(path.join(dir, 'center.jsonl'), '');
  fs.writeFileSync(
    path.join(dir, 'node-n1.jsonl'),
    JSON.stringify({ t: 'spend-accepted', requestId: 'x', cost: 150, windowId: 0, allocs: [], tick: 1 }) + '\n',
  );
  fs.writeFileSync(
    path.join(dir, 'node-n2.jsonl'),
    JSON.stringify({ t: 'spend-accepted', requestId: 'x', cost: 150, windowId: 0, allocs: [], tick: 2 }) + '\n',
  );
  const report = auditDir(dir, 10, 100);
  assert.equal(report.ok, false);
  assert.deepEqual(report.duplicateRequestIds, ['x']);
  assert.equal(report.acceptedUnique, 1); // 同 ID 只计一次
  assert.ok(report.violations.some((v) => v.includes('exceeds budget')));
});

test('demo scenario: deterministic replay and clean audit', () => {
  const dir1 = tmpdir();
  const dir2 = tmpdir();
  const s1 = new Sim(demoScenario, dir1);
  s1.run();
  const s2 = new Sim(demoScenario, dir2);
  s2.run();
  assert.equal(s1.transcript.join('\n'), s2.transcript.join('\n'));
  const report = auditDir(dir1, demoScenario.windowLength, demoScenario.windowBudget);
  assert.equal(report.ok, true, JSON.stringify(report.violations));
  const w0 = report.windows.find((w) => w.windowId === 0);
  const w1 = report.windows.find((w) => w.windowId === 1);
  assert.equal(w0?.consumed, 250);
  assert.equal(w0?.idle, 150);
  assert.equal(w1?.consumed, 250);
  assert.equal(w1?.idle, 150);
  assert.equal(report.conflicts, 1);
  assert.equal(report.rejections['insufficient-quota'], 1);
  assert.equal(report.rejections['no-active-grants'], 2);
  assert.equal(report.unconfirmedReturns.length, 0);
  assert.ok(report.lostGrants.some((g) => g.grantId === 'G2'));
  assert.ok(report.lostGrants.some((g) => g.grantId === 'G3'));
});
