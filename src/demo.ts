import { Scenario } from './types';

/**
 * 演示场景（13 节点）：
 * - app 需从 eng 迁到 ops（继承权限从 deploy 变为 monitor，产生例外）
 * - batch 需从 ops 迁到 archive
 * - proj 需迁到自己的子节点 team-b 之下 —— 形成环，需额外移动 team-b 破环
 * - eng-tools / archive / shared / legacy 固定不可移动
 */
export const demoScenario: Scenario = {
  name: 'demo-reorg',
  nodes: [
    { id: 'root', parent: null, fixed: true, grants: ['read'] },
    { id: 'eng', parent: 'root', grants: ['deploy'] },
    { id: 'ops', parent: 'root', grants: ['monitor'] },
    { id: 'archive', parent: 'root', fixed: true },
    {
      id: 'app',
      parent: 'eng',
      refs: ['/shared/lib', 'eng-tools'],
      targetParent: 'ops',
    },
    { id: 'widget', parent: 'app', refs: ['../../shared/lib'] },
    { id: 'eng-tools', parent: 'eng', fixed: true, grants: ['build'] },
    { id: 'batch', parent: 'ops', refs: ['../eng/app'], targetParent: 'archive' },
    { id: 'proj', parent: 'root', targetParent: 'team-b' },
    { id: 'team-b', parent: 'proj', refs: ['../eng'] },
    { id: 'shared', parent: 'root', fixed: true },
    { id: 'lib', parent: 'shared', grants: ['read:lib'] },
    { id: 'legacy', parent: 'archive', fixed: true, refs: ['/eng/app'] },
  ],
};
