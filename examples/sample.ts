import { diffTrees } from '../src/diff.ts';
import { applyPatch } from '../src/apply.ts';
import { canonicalize } from '../src/util.ts';
import type { TreeNode } from '../src/types.ts';

const before: TreeNode = {
  id: 'app',
  attrs: { title: 'Dashboard', theme: 'light' },
  children: [
    {
      id: 'sidebar',
      children: [
        { id: 'menu', attrs: { collapsed: false } },
        { id: 'trash', children: [{ id: 'archived-report' }, { id: 'stale-draft' }] },
      ],
    },
    { id: 'content', attrs: { layout: 'grid' } },
  ],
};

const after: TreeNode = {
  id: 'app',
  attrs: { title: 'Dashboard', theme: 'dark', version: 2 },
  children: [
    { id: 'content', attrs: { layout: 'list' } },
    {
      id: 'sidebar',
      children: [
        { id: 'menu', attrs: { collapsed: true } },
        // 'trash' is deleted, but 'archived-report' was moved out first.
        { id: 'archived-report' },
      ],
    },
    { id: 'footer', attrs: { sticky: true }, children: [{ id: 'status' }] },
  ],
};

const patch = diffTrees(before, after);
console.log('=== patch (JSON) ===');
console.log(JSON.stringify(patch, null, 2));

const result = applyPatch(before, patch);
console.log('\n=== applied result ===');
console.log(JSON.stringify(result, null, 2));

const ok = JSON.stringify(canonicalize(result)) === JSON.stringify(canonicalize(after));
console.log(`\nround trip matches target: ${ok}`);
if (!ok) process.exit(1);
