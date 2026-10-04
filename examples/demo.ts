/**
 * 端到端示例：用户 u1 从「后端组」迁入「销售部」，
 * 同时组织被重命名、删除了一条 allow 例外、新增了一条 deny 例外。
 */
import { diffSnapshots, formatChain } from '../src/index.ts';
import type { Snapshot, SourcedPath } from '../src/index.ts';

const catalog = { folder: ['/docs', '/docs/api', '/docs/guide', '/code', '/code/src', '/code/tests'] };

const before: Snapshot = {
  tree: {
    rootId: 'root',
    nodes: new Map([
      ['root', { id: 'root', name: '总部', parentId: null }],
      ['eng', { id: 'eng', name: '工程部', parentId: 'root' }],
      ['backend', { id: 'backend', name: '后端组', parentId: 'eng' }],
      ['sales', { id: 'sales', name: '销售部', parentId: 'root' }],
    ]),
  },
  users: [{ id: 'u1', orgId: 'backend' }],
  grants: [
    { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
    { id: 'g-sales', orgId: 'sales', resourceType: 'folder', path: '/docs', actions: ['read'] },
  ],
  exceptions: [
    { id: 'ex-tmp', userId: 'u1', resourceType: 'folder', path: '/docs/api', effect: 'allow', actions: ['write'] },
  ],
  catalog,
};

const after: Snapshot = {
  tree: {
    rootId: 'root',
    nodes: new Map([
      ['root', { id: 'root', name: '集团总部', parentId: null }], // 重命名：不算变化
      ['eng', { id: 'eng', name: '研发部', parentId: 'root' }], // 重命名：不算变化
      ['backend', { id: 'backend', name: '后端组', parentId: 'eng' }],
      ['sales', { id: 'sales', name: '销售部', parentId: 'root' }],
    ]),
  },
  users: [{ id: 'u1', orgId: 'sales' }],
  grants: [
    { id: 'g-eng', orgId: 'eng', resourceType: 'folder', path: '/code', actions: ['read'] },
    { id: 'g-sales', orgId: 'sales', resourceType: 'folder', path: '/docs', actions: ['read'] },
  ],
  exceptions: [
    // ex-tmp 被删除：/docs/api 的 write 随之失去
    { id: 'ex-deny', userId: 'u1', resourceType: 'folder', path: '/docs/guide', effect: 'deny', actions: ['read'] },
  ],
  catalog,
};

const report = diffSnapshots(before, after);
for (const user of report.users) {
  console.log(`用户 ${user.userId} / 资源类型 ${user.resourceType}${user.hasChanges ? '' : '（无变化）'}`);
  for (const d of user.actions) {
    const show = (label: string, items: SourcedPath[]) => {
      for (const it of items) {
        console.log(`  ${label} [${d.action}] ${it.path}`);
        if (it.chain.length > 0) console.log(`      来源: ${formatChain(it.chain)}`);
      }
    };
    show('+ 新增', d.added);
    show('- 失去', d.lost);
    show('= 不变', d.unchanged);
  }
}
