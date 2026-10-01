/**
 * 验收场景演示：
 *   1. 客户端从副本1读取 Key（空上下文）并写入 → (R1:1)
 *   2. 携带上下文的并发覆盖写入            → (R1:2)
 *   3. 离线客户端在副本2写入               → (R1:1, R2:1)
 *   4. 合并器保留两个同名分支（Sibling），等待上层业务解决
 */

import { Replica } from '../src/replica.ts';
import { VersionVector } from '../src/version-vector.ts';
import { syncOneWay } from '../src/synchronizer.ts';

const show = (label: string, replica: Replica, key: string) => {
  const { siblings, context } = replica.get(key);
  const branches = siblings.length
    ? siblings.map((s) => `value=${JSON.stringify(s.value)} @ ${s.version}`).join(' | ')
    : '(不存在)';
  console.log(`  [${label}] ${replica.id} "${key}" → ${branches}  context=${context}`);
};

const r1 = new Replica('R1');
const r2 = new Replica('R2');
const KEY = 'user:42:profile';

console.log('① 客户端从 R1 读取（键不存在，context 为空），写入 v1:');
const ctx0 = r1.get(KEY).context;
r1.put(KEY, { name: 'Ada' }, ctx0);
show('写入后', r1, KEY);

console.log('\n② R1 → R2 同步后，客户端携带 context={R1:1} 覆盖写入 v2:');
syncOneWay(r1, r2);
const ctx1 = r1.get(KEY).context;
const overwrite = r1.put(KEY, { name: 'Ada', lang: 'TS' }, ctx1);
console.log(`  put 结果: ${overwrite.outcome}, version=${overwrite.version}`);
show('覆盖后', r1, KEY);

console.log('\n③ 离线客户端（仅持有过期 context={R1:1}）在 R2 上写入:');
const offlineCtx = VersionVector.from({ R1: 1 });
const offline = r2.put(KEY, { name: 'Ada', lang: 'Go' }, offlineCtx);
console.log(`  put 结果: ${offline.outcome}, version=${offline.version}`);
show('离线写后', r2, KEY);

console.log('\n④ 合并器 R2 → R1 同步：版本并发 → 保留同名分支（Sibling）:');
const report = syncOneWay(r2, r1);
for (const k of report.keys) {
  console.log(`  key="${k.key}" action=${k.action} siblings=${k.siblingCount}`);
}
show('合并后', r1, KEY);

console.log('\n⑤ 上层业务解决冲突并回写，双副本收敛:');
r1.resolve(KEY, { name: 'Ada', lang: 'TS+Go' });
syncOneWay(r1, r2);
show('解决后 R1', r1, KEY);
show('解决后 R2', r2, KEY);
