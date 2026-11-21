import assert from 'node:assert/strict';
import {mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {afterEach, beforeEach, describe, test} from 'node:test';
import {
  AuditLog,
  GENESIS_PREV_HASH,
  generateAuditKeyPair,
  tamperAuditFile,
  verifyAuditFile,
  type AuditEvent,
} from '../src/audit.ts';

function sampleEvent(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    subjectId: 'u-1001',
    subjectName: 'analyst',
    action: 'READ',
    resource: 'customer/c-001',
    requestedFields: ['id', 'email'],
    allowed: true,
    reason: 'READ allowed',
    violatingFields: [],
    ...overrides,
  };
}

describe('AuditLog 不可否认留痕', () => {
  let dir: string;
  let filePath: string;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), 'audit-'));
    filePath = join(dir, 'audit.jsonl');
  });

  afterEach(async () => {
    await rm(dir, {recursive: true, force: true});
  });

  test('每条留痕带连续序号、哈希链与 Ed25519 签名', async () => {
    const keyPair = generateAuditKeyPair();
    const log = AuditLog.open(filePath, keyPair);

    const first = await log.append(sampleEvent());
    const second = await log.append(sampleEvent({allowed: false, action: 'EXPORT'}));

    assert.equal(first.seq, 0);
    assert.equal(first.prevHash, GENESIS_PREV_HASH);
    assert.equal(second.seq, 1);
    assert.equal(second.prevHash, first.entryHash);
    assert.match(first.signature, /^[A-Za-z0-9+/]+=*$/);
    assert.match(first.entryHash, /^[0-9a-f]{64}$/);
  });

  test('未篡改的日志通过完整性校验', async () => {
    const keyPair = generateAuditKeyPair();
    const log = AuditLog.open(filePath, keyPair);
    await log.append(sampleEvent());
    await log.append(sampleEvent({allowed: false}));

    assert.deepEqual(log.verifyIntegrity(), {valid: true, entries: 2});
    assert.deepEqual(verifyAuditFile(filePath, keyPair.publicKey), {valid: true, entries: 2});
  });

  test('服务重启后在同一条哈希链上续写', async () => {
    const keyPair = generateAuditKeyPair();
    const firstLog = AuditLog.open(filePath, keyPair);
    const first = await firstLog.append(sampleEvent());

    const reopened = AuditLog.open(filePath, keyPair);
    const second = await reopened.append(sampleEvent());

    assert.equal(second.seq, 1);
    assert.equal(second.prevHash, first.entryHash);
    assert.equal(reopened.verifyIntegrity().valid, true);
  });

  test('篡改历史条目的判定结果会被完整性校验发现', async () => {
    const keyPair = generateAuditKeyPair();
    const log = AuditLog.open(filePath, keyPair);
    await log.append(sampleEvent());
    await log.append(sampleEvent({allowed: false, reason: 'Cross-level READ denied'}));

    // 试图把一条“拒绝”留痕抹成“允许”。
    tamperAuditFile(filePath, 1, {allowed: true, reason: 'READ allowed'});

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, false);
    assert.equal(report.error?.seq, 1);
    assert.equal(report.error?.kind, 'DIGEST_MISMATCH');
  });

  test('删除历史条目导致链断裂被发现', async () => {
    const keyPair = generateAuditKeyPair();
    const log = AuditLog.open(filePath, keyPair);
    await log.append(sampleEvent());
    await log.append(sampleEvent());
    await log.append(sampleEvent());

    const {readFileSync, writeFileSync} = await import('node:fs');
    const lines = readFileSync(filePath, 'utf8')
      .split('\n')
      .filter((line) => line.length > 0);
    writeFileSync(filePath, [lines[0], lines[2]].join('\n') + '\n');

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, false);
    assert.equal(report.error?.kind, 'BAD_SEQUENCE');
  });

  test('伪造签名（用另一把私钥重签）无法通过公钥校验', async () => {
    const keyPair = generateAuditKeyPair();
    const attacker = generateAuditKeyPair();
    const log = AuditLog.open(filePath, keyPair);
    await log.append(sampleEvent());

    const {readFileSync, writeFileSync} = await import('node:fs');
    const entry = JSON.parse(readFileSync(filePath, 'utf8').trim()) as import('../src/audit.ts').AuditEntry;
    const {sign} = await import('node:crypto');
    const forgedSignature = sign(null, Buffer.from(entry.entryHash, 'hex'), attacker.privateKey)
      .toString('base64');
    writeFileSync(filePath, JSON.stringify({...entry, signature: forgedSignature}) + '\n');

    const report = verifyAuditFile(filePath, keyPair.publicKey);
    assert.equal(report.valid, false);
    assert.equal(report.error?.kind, 'SIGNATURE_INVALID');
  });
});
