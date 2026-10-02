import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LSMEngine } from '../src/engine.ts';

const dir = mkdtempSync(join(tmpdir(), 'lsm-demo-'));
console.log(`数据目录: ${dir}\n`);

// 1. 单个 LSM 实例中创建两个逻辑隔离的列族
let engine = LSMEngine.open(dir);
const cfDefault = engine.createColumnFamily('CF_Default');
const cfMeta = engine.createColumnFamily('CF_Metadata');
console.log('列族:', engine.listColumnFamilies());

// 2. 单次原子事务同时写入两个列族（共享一份物理 WAL）
engine.writeBatch([
  { cf: cfDefault, key: 'user:1001', value: '{"name":"alice"}' },
  { cf: cfMeta, key: 'table:users:schema_version', value: '3' },
  { cf: cfMeta, key: 'table:users:row_count', value: '1' },
]);
console.log('\n[原子事务] 已提交，WAL 文件:', join(dir, 'wal.log'));

// 3. 列族隔离：CF_Metadata 只能看到本列族数据
console.log('\n[隔离性]');
console.log('  CF_Metadata table:users:schema_version =',
  engine.get(cfMeta, 'table:users:schema_version'));
console.log('  CF_Metadata user:1001                =',
  engine.get(cfMeta, 'user:1001'), '(不可见，属于 CF_Default)');
console.log('  CF_Default  user:1001                =',
  engine.get(cfDefault, 'user:1001'));

// 4. 单列族独立快照
const snap = engine.snapshot(cfDefault);
engine.put(cfDefault, 'user:1001', '{"name":"alice","age":30}');
console.log('\n[快照]');
console.log('  快照读取 user:1001 =', engine.get(cfDefault, 'user:1001', { snapshot: snap }));
console.log('  实时读取 user:1001 =', engine.get(cfDefault, 'user:1001'));

// 5. 模拟崩溃重启：从共享 WAL 恢复，两个列族原子对齐
engine.flush(cfDefault);
engine = LSMEngine.open(dir);
console.log('\n[恢复] 重启后（WAL 重放 + SSTable 加载）:');
console.log('  CF_Default  user:1001                =',
  engine.get({ name: 'CF_Default' }, 'user:1001'));
console.log('  CF_Metadata table:users:schema_version =',
  engine.get({ name: 'CF_Metadata' }, 'table:users:schema_version'));
console.log('  CF_Metadata table:users:row_count      =',
  engine.get({ name: 'CF_Metadata' }, 'table:users:row_count'));

rmSync(dir, { recursive: true, force: true });
console.log('\n演示完成。');
