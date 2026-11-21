import {rmSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {
  AuditLog,
  exportPublicKeyPem,
  generateAuditKeyPair,
  tamperAuditFile,
  verifyAuditFile,
} from './audit.ts';
import {AccessControlService} from './access-control.ts';
import {Level} from './levels.ts';
import type {FieldSchema, Subject} from './policy.ts';

const logPath = fileURLToPath(new URL('../logs/audit-demo.jsonl', import.meta.url));
rmSync(logPath, {force: true});

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

const keyPair = generateAuditKeyPair();
const service = new AccessControlService(schema, AuditLog.open(logPath, keyPair));

function line(title: string): void {
  console.log(`\n=== ${title} ===`);
}

line('平台审计公钥（验签方持有）');
console.log(exportPublicKeyPem(keyPair).trim());

line('场景 1：合法读取 id/name/email（均 <= CONFIDENTIAL）');
const legalRead = await service.readRecord(analyst, 'customer/c-001', record, ['id', 'name', 'email']);
console.log('判定:', legalRead.decision.allowed, '| 返回数据:', legalRead.data);

line('场景 2：越级读取 bankAccount（SECRET > CONFIDENTIAL）');
const crossRead = await service.readRecord(analyst, 'customer/c-001', record, ['name', 'bankAccount'], {
  origin: 'web-console/10.0.0.5',
});
console.log('判定:', crossRead.decision.allowed, '| 返回数据:', crossRead.data);
console.log('拒绝原因:', crossRead.decision.reason);

line('场景 3：合法批量导出 id/name（<= INTERNAL 导出级别）');
const legalExport = await service.exportRecords(analyst, 'customer/export', [record], ['id', 'name']);
console.log('判定:', legalExport.decision.allowed, '| 导出数据:', legalExport.data);

line('场景 4：危险导出组合 id + email（email 超出导出级别）');
const badExport = await service.exportRecords(analyst, 'customer/export', [record], ['id', 'email']);
console.log('判定:', badExport.decision.allowed, '| 导出数据:', badExport.data);
console.log('拒绝原因:', badExport.decision.reason);

line('留痕完整性校验（4 条访问，允许/拒绝均留痕）');
const before = verifyAuditFile(logPath, keyPair.publicKey);
console.log(before);

line('篡改演练：将 seq=1 的越级拒绝留痕伪造成“允许”');
tamperAuditFile(logPath, 1, {allowed: true, reason: 'READ allowed'});
const after = verifyAuditFile(logPath, keyPair.publicKey);
console.log(after);

line(`留痕文件: ${logPath}`);
