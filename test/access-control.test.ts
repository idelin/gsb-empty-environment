import assert from 'node:assert/strict';
import {mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {afterEach, beforeEach, describe, test} from 'node:test';
import {AuditLog, generateAuditKeyPair, verifyAuditFile} from '../src/audit.ts';
import {AccessControlService} from '../src/access-control.ts';
import {Level} from '../src/levels.ts';
import {PolicyViolationError, type FieldSchema, type Subject} from '../src/policy.ts';

const schema: FieldSchema = {
  id: Level.PUBLIC,
  name: Level.INTERNAL,
  email: Level.CONFIDENTIAL,
  bankAccount: Level.SECRET,
};

const analyst: Subject = {
  id: 'u-1001',
  name: 'analyst-li',
  readClearance: Level.CONFIDENTIAL,
  exportClearance: Level.INTERNAL,
};

const record = {
  id: 'c-001',
  name: 'Zhang San',
  email: 'zhangsan@example.com',
  bankAccount: '6222-0000-1111-2222',
};

describe('AccessControlService 端到端判定与留痕', () => {
  let dir: string;
  let filePath: string;
  let service: AccessControlService;
  let keyPair: ReturnType<typeof generateAuditKeyPair>;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), 'ac-'));
    filePath = join(dir, 'audit.jsonl');
    keyPair = generateAuditKeyPair();
    service = new AccessControlService(schema, AuditLog.open(filePath, keyPair));
  });

  afterEach(async () => {
    await rm(dir, {recursive: true, force: true});
  });

  test('合法读取：返回字段投影并写入 ALLOW 留痕', async () => {
    const outcome = await service.readRecord(analyst, 'customer/c-001', record, ['id', 'name', 'email']);

    assert.equal(outcome.decision.allowed, true);
    assert.deepEqual(outcome.data, {
      id: 'c-001',
      name: 'Zhang San',
      email: 'zhangsan@example.com',
    });
    assert.equal(outcome.auditEntry.allowed, true);
    assert.equal(outcome.auditEntry.action, 'READ');

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, true);
    assert.equal(report.entries, 1);
  });

  test('越级读取：拒绝返回数据并写入 DENY 留痕（含越级字段）', async () => {
    const outcome = await service.readRecord(
      analyst,
      'customer/c-001',
      record,
      ['name', 'bankAccount'],
      {origin: 'web-console/10.0.0.5'},
    );

    assert.equal(outcome.decision.allowed, false);
    assert.equal(outcome.data, null);
    assert.equal(outcome.auditEntry.allowed, false);
    assert.deepEqual(outcome.auditEntry.violatingFields, ['bankAccount']);
    assert.equal(outcome.auditEntry.origin, 'web-console/10.0.0.5');
    assert.match(outcome.auditEntry.reason, /bankAccount\[SECRET\]/);

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, true);
    assert.equal(report.entries, 1);
  });

  test('越级读取可配置为抛出 PolicyViolationError，且留痕依然落盘', async () => {
    await assert.rejects(
      service.readRecord(analyst, 'customer/c-001', record, ['bankAccount'], {throwOnDenied: true}),
      (error: unknown) => error instanceof PolicyViolationError && error.decision.allowed === false,
    );
    assert.equal(verifyAuditFile(filePath, keyPair.publicKey).entries, 1);
  });

  test('合法批量导出：仅输出导出级别内的字段组合', async () => {
    const outcome = await service.exportRecords(
      analyst,
      'customer/export',
      [record, {...record, id: 'c-002', name: 'Li Si'}],
      ['id', 'name'],
    );

    assert.equal(outcome.decision.allowed, true);
    assert.deepEqual(outcome.data, [
      {id: 'c-001', name: 'Zhang San'},
      {id: 'c-002', name: 'Li Si'},
    ]);
    assert.equal(outcome.auditEntry.action, 'EXPORT');
  });

  test('危险导出组合：高敏字段混入时整批拒绝且不泄露任何行数据', async () => {
    const outcome = await service.exportRecords(
      analyst,
      'customer/export',
      [record],
      ['id', 'email'],
    );

    assert.equal(outcome.decision.allowed, false);
    assert.equal(outcome.data, null);
    assert.deepEqual(outcome.auditEntry.violatingFields, ['email']);
    assert.match(outcome.auditEntry.reason, /Bulk EXPORT denied/);
  });

  test('混合访问序列：允许与拒绝均留痕，整条链完整性可验证', async () => {
    await service.readRecord(analyst, 'customer/c-001', record, ['id']);
    await service.readRecord(analyst, 'customer/c-001', record, ['bankAccount']);
    await service.exportRecords(analyst, 'customer/export', [record], ['id', 'name']);
    await service.exportRecords(analyst, 'customer/export', [record], ['email']);

    const entries = AuditLog.open(filePath, keyPair).readAll();
    assert.deepEqual(
      entries.map((entry) => [entry.action, entry.allowed]),
      [
        ['READ', true],
        ['READ', false],
        ['EXPORT', true],
        ['EXPORT', false],
      ],
    );

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, true);
    assert.equal(report.entries, 4);
  });
});
