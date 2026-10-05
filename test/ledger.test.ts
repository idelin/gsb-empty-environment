import test from 'node:test';
import assert from 'node:assert/strict';
import {
  AlreadyRevokedError,
  BenefitLedger,
  InsufficientQuotaError,
  UnknownBenefitTypeError,
  UnknownConsumptionError,
  ValidationError,
} from '../src/index.ts';

function makeStackLedger(): BenefitLedger {
  const ledger = new BenefitLedger();
  ledger.registerBenefitType('massage', 'stack');
  return ledger;
}

test('stack 规则：多套餐次数叠加，按先到期先用的确定顺序扣减', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P2', validFrom: 0, validTo: 200, grants: { massage: 10 } });
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 10 } });

  assert.equal(ledger.available('massage', 10), 20);

  const c = ledger.consume({ benefitType: 'massage', quantity: 12, at: 10 });
  assert.deepEqual(c.allocations, [
    { packageId: 'P1', quantity: 10 },
    { packageId: 'P2', quantity: 2 },
  ]);
  assert.equal(ledger.available('massage', 10), 8);

  const balance = ledger.getBalance('massage', 10);
  assert.deepEqual(
    balance.packages.map((p) => [p.packageId, p.consumed, p.remaining]),
    [['P1', 10, 0], ['P2', 2, 8]],
  );
});

test('确定性：相同输入重复执行得到完全一致的分配', () => {
  const run = () => {
    const ledger = makeStackLedger();
    ledger.addPackage({ id: 'PB', validFrom: 0, validTo: 100, grants: { massage: 5 } });
    ledger.addPackage({ id: 'PA', validFrom: 0, validTo: 100, grants: { massage: 5 } });
    ledger.addPackage({ id: 'PC', validFrom: 0, validTo: 50, grants: { massage: 5 } });
    return ledger.consume({ benefitType: 'massage', quantity: 8, at: 10 }).allocations;
  };
  const first = run();
  // 到期日相同则按 validFrom、再按 packageId 字典序，结果与注册顺序无关
  assert.deepEqual(first, [
    { packageId: 'PC', quantity: 5 },
    { packageId: 'PA', quantity: 3 },
  ]);
  assert.deepEqual(run(), first);
});

test('max 规则：总额度取各套餐最大值而非求和', () => {
  const ledger = new BenefitLedger();
  ledger.registerBenefitType('pool', 'max');
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { pool: 10 } });
  ledger.addPackage({ id: 'P2', validFrom: 0, validTo: 100, grants: { pool: 6 } });

  assert.equal(ledger.available('pool', 10), 10);

  const c = ledger.consume({ benefitType: 'pool', quantity: 10, at: 10 });
  assert.deepEqual(c.allocations, [{ packageId: 'P1', quantity: 10 }]);
  assert.equal(ledger.available('pool', 10), 0);

  assert.throws(
    () => ledger.consume({ benefitType: 'pool', quantity: 1, at: 10 }),
    InsufficientQuotaError,
  );
});

test('max 规则：额度归属当前有效套餐集合，过期套餐的消耗不再占用', () => {
  const ledger = new BenefitLedger();
  ledger.registerBenefitType('pool', 'max');
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 50, grants: { pool: 10 } });
  ledger.addPackage({ id: 'P2', validFrom: 40, validTo: 100, grants: { pool: 10 } });

  ledger.consume({ benefitType: 'pool', quantity: 8, at: 10 });
  assert.equal(ledger.available('pool', 45), 2);
  // P1 过期后，其历史消耗不再占用 P2 所在集合的额度
  assert.equal(ledger.available('pool', 60), 10);
});

test('有效期之外不可消费，过期套餐不计入额度', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 10, validTo: 20, grants: { massage: 5 } });

  assert.equal(ledger.available('massage', 9), 0);
  assert.equal(ledger.available('massage', 20), 0);
  assert.throws(
    () => ledger.consume({ benefitType: 'massage', quantity: 1, at: 20 }),
    InsufficientQuotaError,
  );
});

test('额度不足时整体失败，不产生部分扣减', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 3 } });

  assert.throws(
    () => ledger.consume({ benefitType: 'massage', quantity: 4, at: 10 }),
    (err: unknown) => {
      assert.ok(err instanceof InsufficientQuotaError);
      assert.equal(err.requested, 4);
      assert.equal(err.available, 3);
      return true;
    },
  );
  assert.equal(ledger.getConsumptionHistory().length, 0);
  assert.equal(ledger.available('massage', 10), 3);
});

test('撤销把次数精确返还原来源套餐', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 10 } });
  ledger.addPackage({ id: 'P2', validFrom: 0, validTo: 200, grants: { massage: 10 } });

  const c = ledger.consume({ benefitType: 'massage', quantity: 12, at: 10 });
  const revocation = ledger.revoke(c.id, 30);

  assert.deepEqual(
    revocation.returns.map((r) => [r.packageId, r.quantity, r.expiredOnReturn]),
    [['P1', 10, false], ['P2', 2, false]],
  );
  assert.equal(ledger.available('massage', 30), 20);
  assert.equal(ledger.getConsumption(c.id).status, 'revoked');
  assert.equal(ledger.getConsumptionHistory({ status: 'confirmed' }).length, 0);
  assert.equal(ledger.getConsumptionHistory({ status: 'revoked' }).length, 1);
});

test('撤销不能延长有效期：来源已过期时返还次数不可用', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 50, grants: { massage: 5 } });

  const c = ledger.consume({ benefitType: 'massage', quantity: 5, at: 10 });
  const revocation = ledger.revoke(c.id, 60);

  assert.equal(revocation.returns[0]?.expiredOnReturn, true);
  assert.deepEqual(revocation.returns[0]?.usableAt, { validFrom: 0, validTo: 50 });
  // 返还发生在有效期之外，可用额度不因撤销而复活
  assert.equal(ledger.available('massage', 60), 0);
  assert.throws(
    () => ledger.consume({ benefitType: 'massage', quantity: 1, at: 60 }),
    InsufficientQuotaError,
  );
});

test('重复撤销报错', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 5 } });
  const c = ledger.consume({ benefitType: 'massage', quantity: 1, at: 10 });
  ledger.revoke(c.id, 20);
  assert.throws(() => ledger.revoke(c.id, 21), AlreadyRevokedError);
  assert.throws(() => ledger.revoke('nope', 21), UnknownConsumptionError);
});

test('追溯更正不会悄悄改写已确认消费的分配', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 10 } });
  ledger.addPackage({ id: 'P2', validFrom: 0, validTo: 200, grants: { massage: 10 } });

  ledger.consume({ benefitType: 'massage', quantity: 12, at: 10, id: 'ORDER-1' });
  const snapshotBefore = JSON.parse(JSON.stringify(ledger.getConsumptionHistory()));

  // 追溯把 P1 的额度从 10 改为 4，并把 P2 的有效期缩短
  ledger.correctPackage('P1', { grants: { massage: 4 } }, { at: 20, reason: '开卡数量录错' });
  ledger.correctPackage('P2', { validTo: 150 }, { at: 21 });

  // 1. 历史查询结果与更正前逐字节一致：分配没有被改写
  const historyAfter = ledger.getConsumptionHistory();
  assert.deepEqual(JSON.parse(JSON.stringify(historyAfter)), snapshotBefore);
  assert.deepEqual(ledger.getConsumption('ORDER-1').allocations, [
    { packageId: 'P1', quantity: 10 },
    { packageId: 'P2', quantity: 2 },
  ]);

  // 2. 更正造成 P1 已确认消耗(10)超过新额度(4)，缺口被显式暴露而非抹平
  const balance = ledger.getBalance('massage', 30);
  const p1 = balance.packages.find((p) => p.packageId === 'P1');
  assert.equal(p1?.quota, 4);
  assert.equal(p1?.consumed, 10);
  assert.equal(p1?.remaining, 0);
  assert.equal(p1?.deficit, 6);
  assert.equal(balance.deficit, 6);
  assert.equal(balance.available, 8);

  // 3. 更正本身留有审计痕迹，旧版本可查
  const corrections = ledger.getPackageCorrections('P1');
  assert.equal(corrections.length, 1);
  assert.equal(corrections[0]?.previous.grants['massage'], 10);
  assert.equal(corrections[0]?.next.grants['massage'], 4);
  assert.equal(corrections[0]?.reason, '开卡数量录错');
  assert.ok(
    ledger.getEvents().some((e) => e.kind === 'package_corrected' && e.packageId === 'P1'),
  );
});

test('追溯更正缩短有效期后，原区间内确认的消费仍然保留', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 5 } });
  const c = ledger.consume({ benefitType: 'massage', quantity: 5, at: 10 });

  ledger.correctPackage('P1', { validTo: 30 }, { at: 20 });

  // 消费记录原样保留
  assert.equal(ledger.getConsumption(c.id).status, 'confirmed');
  assert.deepEqual(ledger.getConsumption(c.id).allocations, [
    { packageId: 'P1', quantity: 5 },
  ]);
  // 但 30 之后该套餐不再提供额度
  assert.equal(ledger.available('massage', 30), 0);
  const balance = ledger.getBalance('massage', 30);
  assert.equal(balance.packages[0]?.active, false);
});

test('历史查询返回快照，外部修改不影响账本', () => {
  const ledger = makeStackLedger();
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 100, grants: { massage: 5 } });
  const c = ledger.consume({ benefitType: 'massage', quantity: 2, at: 10 });

  assert.throws(() => {
    (c.allocations as unknown as Array<unknown>).push({ packageId: 'X', quantity: 1 });
  }, TypeError);

  const history = ledger.getConsumptionHistory();
  (history[0] as { status: string }).status = 'revoked';
  assert.equal(ledger.getConsumption(c.id).status, 'confirmed');
});

test('输入校验：未注册权益、非法区间、非法数量、重复单号', () => {
  const ledger = makeStackLedger();
  assert.throws(
    () => ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 10, grants: { unknown: 1 } }),
    UnknownBenefitTypeError,
  );
  assert.throws(
    () => ledger.addPackage({ id: 'P1', validFrom: 10, validTo: 10, grants: { massage: 1 } }),
    ValidationError,
  );
  assert.throws(
    () => ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 10, grants: { massage: 1.5 } }),
    ValidationError,
  );
  ledger.addPackage({ id: 'P1', validFrom: 0, validTo: 10, grants: { massage: 2 } });
  assert.throws(
    () => ledger.consume({ benefitType: 'massage', quantity: 0, at: 1 }),
    ValidationError,
  );
  ledger.consume({ benefitType: 'massage', quantity: 1, at: 1, id: 'X' });
  assert.throws(
    () => ledger.consume({ benefitType: 'massage', quantity: 1, at: 1, id: 'X' }),
    ValidationError,
  );
});
