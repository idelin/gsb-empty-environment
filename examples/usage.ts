import { inferProtectionClasses } from '../src/index.ts';
import type { InferenceInput } from '../src/index.ts';

const input: InferenceInput = {
  // 应用默认保护类（缺省 create / atomicWrite、copy 副本、restore 都取它）
  defaultClass: 'completeUntilFirstUserAuthentication',
  rules: [
    // 敏感目录要求最低 complete；命中多条规则时取最强要求
    { prefix: '/var/mobile/Containers/secret', minClass: 'complete' },
  ],
  operations: [
    // 1. 以最强类创建敏感文件，合规
    {
      op: 'create',
      path: '/var/mobile/Containers/secret/wallet.dat',
      protectionClass: 'complete',
      id: 'create-wallet',
    },
    // 2. 缺省原子写：回落到默认类，且不继承被替换文件的 complete
    { op: 'atomicWrite', path: '/var/mobile/Containers/secret/wallet.dat', id: 'rewrite-wallet' },

    // 3. 普通目录里的弱类文件
    { op: 'create', path: '/tmp/inbox/note.txt', id: 'create-note' },
    // 4. copy：副本取默认类（不继承源类），随后紧跟 setAttributes 修正
    {
      op: 'copy',
      from: '/tmp/inbox/note.txt',
      path: '/var/mobile/Containers/secret/note-copy.txt',
      id: 'copy-note',
    },
    {
      op: 'setAttributes',
      path: '/var/mobile/Containers/secret/note-copy.txt',
      protectionClass: 'complete',
      id: 'harden-copy',
    },

    // 5. 备份恢复：一律取默认类，丢弃历史 complete
    {
      op: 'restore',
      path: '/var/mobile/Containers/secret/backup.db',
      id: 'restore-backup',
    },

    // 6. move：弱类原样保留并进入受规则目录
    {
      op: 'move',
      from: '/tmp/inbox/note.txt',
      path: '/var/mobile/Containers/secret/note-moved.txt',
      id: 'move-note',
    },
  ],
};

const result = inferProtectionClasses(input);

console.log('路径终态:');
for (const pathResult of result.paths) {
  console.log(
    `  ${pathResult.path} -> ${pathResult.finalClass} ` +
      `(要求 ${pathResult.requiredClass ?? '无'}, ` +
      `${pathResult.compliant ? '合规' : '违规'}, ` +
      `决定步=${pathResult.decidingOperationIndex})`,
  );
}

console.log('\n违规与降级步骤:');
for (const violation of result.violations) {
  console.log(
    `  [步 ${violation.violatingOperationIndex}] ${violation.violatingOperation} ` +
      `${violation.violatingOperationId ?? ''}: ${violation.reason}`,
  );
}
