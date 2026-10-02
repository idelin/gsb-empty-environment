import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AbortError, SsiDatabase, Transaction } from '../src/ssi.ts';

/**
 * 验收场景：账户 A=100，B=100，约束 A+B>=0。
 * T1 读 A、B 并扣 A；T2 并发读 A、B 并扣 B。
 *
 * 注：规范中“各扣 80”时最终 A+B=40 并不违约；扣款额需 >100
 * 才能使双重扣款后的总和为负。主场景取 150 使违约真实发生，
 * 另有用例证明扣 80 时 SSI 同样精准中止 T2（危险结构与金额无关）。
 */
function runWriteSkew(mode: 'si' | 'ssi', deduct: number) {
  const db = new SsiDatabase(mode);
  db.seed('A', 100);
  db.seed('B', 100);

  const t1 = db.begin();
  const t2 = db.begin();

  const a1 = t1.read('A');
  const b1 = t1.read('B');
  const a2 = t2.read('A');
  const b2 = t2.read('B');

  // 两个事务都在各自快照上验证约束 A+B>=0，均通过（100+100-deduct >= 0）。
  assert.ok(a1 + b1 - deduct >= 0);
  assert.ok(a2 + b2 - deduct >= 0);

  t1.write('A', a1 - deduct);
  t2.write('B', b2 - deduct);

  const outcome = { t1: 'committed', t2: 'committed', t2Error: null as AbortError | null };
  t1.commit();
  try {
    t2.commit();
  } catch (err) {
    assert.ok(err instanceof AbortError);
    outcome.t2 = 'aborted';
    outcome.t2Error = err;
  }
  return { db, outcome };
}

test('纯 SI 下发生写倾斜：双重扣款成功，约束 A+B>=0 被破坏', () => {
  const { db, outcome } = runWriteSkew('si', 150);
  assert.equal(outcome.t1, 'committed');
  assert.equal(outcome.t2, 'committed');
  // 异常结果：A=-50, B=-50，A+B=-100 < 0，约束违约。
  assert.equal(db.latest('A'), -50);
  assert.equal(db.latest('B'), -50);
  assert.ok(db.latest('A') + db.latest('B') < 0);
});

test('SSI 精准识别危险结构并中止 T2，T1 正常提交，约束保持', () => {
  const { db, outcome } = runWriteSkew('ssi', 150);
  assert.equal(outcome.t1, 'committed');
  assert.equal(outcome.t2, 'aborted');
  assert.equal(outcome.t2Error!.reason, 'write-skew-danger');
  // 只有 T1 的扣款生效：A=-50, B=100，A+B=50 >= 0。
  assert.equal(db.latest('A'), -50);
  assert.equal(db.latest('B'), 100);
  assert.ok(db.latest('A') + db.latest('B') >= 0);
});

test('规范原始数字（各扣 80）：SSI 同样精准中止 T2（危险结构与金额无关）', () => {
  const { db, outcome } = runWriteSkew('ssi', 80);
  assert.equal(outcome.t1, 'committed');
  assert.equal(outcome.t2, 'aborted');
  assert.equal(outcome.t2Error!.reason, 'write-skew-danger');
  assert.equal(db.latest('A'), 20);
  assert.equal(db.latest('B'), 100);
});

test('SSI 不误伤：无冲突的并发事务都能提交', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);
  db.seed('B', 100);

  const t1 = db.begin();
  const t2 = db.begin();
  t1.write('A', t1.read('A') - 10); // 只碰 A
  t2.write('B', t2.read('B') - 10); // 只碰 B
  t1.commit();
  t2.commit();

  assert.equal(t1.status, 'committed');
  assert.equal(t2.status, 'committed');
  assert.equal(db.latest('A'), 90);
  assert.equal(db.latest('B'), 90);
});

test('SSI 不误伤：写-读（wr 依赖）不构成 rw 反依赖环', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);

  const t1 = db.begin();
  t1.write('A', 50);
  t1.commit();

  const t2 = db.begin(); // 快照在 T1 提交之后，读到新值
  assert.equal(t2.read('A'), 50);
  t2.write('A', 60);
  t2.commit();

  assert.equal(t2.status, 'committed');
  assert.equal(db.latest('A'), 60);
});

test('SI 写-写冲突：先提交者获胜，后写者被中止', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);

  const t1 = db.begin();
  const t2 = db.begin();
  t1.write('A', 1);
  t1.commit();
  assert.throws(() => t2.write('A', 2), (err: unknown) => {
    assert.ok(err instanceof AbortError);
    assert.equal(err.reason, 'write-write-conflict');
    return true;
  });
  assert.equal(t2.status, 'aborted');
  assert.equal(db.latest('A'), 1);
});

test('快照隔离：事务读不到快照之后提交的版本', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);

  const t1 = db.begin();
  const t2 = db.begin();
  t2.write('A', 200);
  t2.commit();

  assert.equal(t1.read('A'), 100); // T1 仍看到旧快照
  t1.abort();
});

test('写倾斜的镜像时序：T2 先提交时中止 T1', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);
  db.seed('B', 100);

  const t1 = db.begin();
  const t2 = db.begin();
  const a1 = t1.read('A');
  t1.read('B');
  t2.read('A');
  const b2 = t2.read('B');
  t1.write('A', a1 - 150);
  t2.write('B', b2 - 150);

  t2.commit(); // T2 先提交
  assert.throws(() => t1.commit(), (err: unknown) => {
    assert.ok(err instanceof AbortError);
    assert.equal(err.reason, 'write-skew-danger');
    return true;
  });
  assert.equal(t2.status, 'committed');
  assert.equal(t1.status, 'aborted');
  assert.equal(db.latest('A'), 100);
  assert.equal(db.latest('B'), -50);
});

test('被中止事务的写不会进入存储', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);
  db.seed('B', 100);

  const t1 = db.begin();
  const t2 = db.begin();
  t1.read('A');
  t1.read('B');
  t2.read('A');
  const b2 = t2.read('B');
  t1.write('A', 20);
  t2.write('B', b2 - 150);
  t1.commit();
  assert.throws(() => t2.commit(), AbortError);

  assert.equal(db.latest('A'), 20);
  assert.equal(db.latest('B'), 100); // T2 的写未生效
});

test('读己之写（read-your-own-writes）', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);
  const t = db.begin();
  t.write('A', 42);
  assert.equal(t.read('A'), 42);
  t.commit();
  assert.equal(db.latest('A'), 42);
});

test('中止后不允许继续操作', () => {
  const db = new SsiDatabase('ssi');
  db.seed('A', 100);
  const t: Transaction = db.begin();
  t.abort();
  assert.throws(() => t.read('A'), /not active/);
  assert.throws(() => t.commit(), /not active/);
});
