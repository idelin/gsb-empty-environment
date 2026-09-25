import { applyPatch, diffTrees, jsonEqual } from '../src/index.ts';

const oldTree = {
  id: 'root',
  title: 'Q1 plan',
  children: [
    {
      id: 'backend',
      owner: 'ada',
      children: [
        { id: 'api', status: 'open' },
        { id: 'db', status: 'open' },
      ],
    },
    {
      id: 'web',
      owner: 'grace',
      children: [{ id: 'ui', status: 'open' }],
    },
  ],
};

const newTree = {
  id: 'root',
  title: 'Q2 plan',
  children: [
    { id: 'web', owner: 'grace' },
    {
      id: 'backend',
      owner: 'lin',
      children: [
        { id: 'db', status: 'done' },
        { id: 'ui', status: 'open' }, // moved from web -> backend
        { id: 'cache', status: 'open' }, // newly inserted
      ],
    },
  ],
};

const ops = diffTrees(oldTree, newTree);
console.log('--- patch ops ---');
console.log(JSON.stringify(ops, null, 2));

const result = applyPatch(oldTree, ops);
console.log('--- patched tree ---');
console.log(JSON.stringify(result, null, 2));
console.log('matches target:', jsonEqual(result, newTree));
