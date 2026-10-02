import { compile, validate, interpret, crossCheck, InstanceStore } from '../src/index.ts';
import type { Schema } from '../src/schema.ts';

function show(title: string, data: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(typeof data === 'string' ? data : JSON.stringify(data, null, 2));
}

// A self-recursive schema: a file-system-like tree.
const treeSchema = {
  $defs: {
    node: {
      type: 'object',
      required: ['name'],
      properties: {
        name: { type: 'string' },
        size: { oneOf: [{ type: 'number' }, { type: 'null' }] },
        children: { type: 'array', items: { $ref: '#/$defs/node' } },
      },
    },
  },
  $ref: '#/$defs/node',
} as unknown as Schema;

// 1. Compile to a shared validation graph.
const graph = compile(treeSchema);
const rootNode = graph.nodes[graph.root];
const childrenNode = graph.nodes[(rootNode as { props: Map<string, number> }).props.get('children')!];
show('compiled graph', {
  nodeCount: graph.nodes.length,
  kinds: graph.nodes.map((n) => `${n.id}:${n.kind}@${n.schemaPath}`),
  'children.items points back to root': (childrenNode as { items: number }).items === graph.root,
});

// 2. Validate a good instance.
const good = {
  name: 'root',
  size: null,
  children: [
    { name: 'README.md', size: 120 },
    { name: 'src', size: null, children: [{ name: 'index.ts', size: 42 }] },
  ],
};
show('valid instance', validate(graph, good));

// 3. Validate a broken instance: errors carry instance path AND schema path.
const bad = {
  name: 'root',
  size: 'big', // not number|null
  children: [{ size: 1 }], // missing required "name"
};
show('invalid instance (errors with instancePath + schemaPath)', validate(graph, bad));

// 4. Depth budget: exhaustion is "incomplete", not a schema failure.
let deep: unknown = { name: 'leaf' };
for (let i = 0; i < 30; i++) deep = { name: `d${i}`, children: [deep] };
show('depth budget exhausted -> incomplete', validate(graph, deep, { maxDepth: 8 }));

// 5. Error budget: truncation is "incomplete" too.
const manyBad = { name: 'r', children: Array.from({ length: 20 }, () => ({ name: 1 })) };
show('error budget exhausted -> incomplete', validate(graph, manyBad, { maxErrors: 3 }));

// 6. Incremental updates with atomic error/index swaps.
const store = new InstanceStore(graph, {
  name: 'root',
  children: [{ size: 5 }, { name: 'ok', size: null }],
});
show('incremental: initial errors', store.errors);
store.set('/children/0/name', 'fixed');
show('incremental: after fixing /children/0/name', {
  verdict: store.verdict,
  errors: store.errors,
  errorsAtChild0: store.errorsAt('/children/0'),
});
store.set('/children/1/size', 'oops');
show('incremental: after breaking /children/1/size', {
  verdict: store.verdict,
  errors: store.errors,
});

// 7. Independent interpreter cross-check on small bounded instances.
show('cross-check: graph validator vs independent interpreter', {
  good: crossCheck(graph, treeSchema, good).consistent,
  bad: crossCheck(graph, treeSchema, bad).consistent,
  starvedBudget: crossCheck(graph, treeSchema, good, {}, { maxSteps: 2 }).interpreter.verdict,
});

// 8. Compile-time rejections.
for (const [label, schema] of [
  ['dangling ref', { $ref: '#/$defs/nope' }],
  ['non-productive ref cycle', { $defs: { a: { $ref: '#/$defs/b' }, b: { $ref: '#/$defs/a' } }, $ref: '#/$defs/a' }],
  ['non-consuming oneOf cycle', { $defs: { u: { oneOf: [{ $ref: '#/$defs/u' }, { type: 'string' }] } }, $ref: '#/$defs/u' }],
] as Array<[string, unknown]>) {
  try {
    compile(schema as Schema);
    console.log(`\n=== ${label} ===\nNOT REJECTED (bug!)`);
  } catch (err) {
    console.log(`\n=== ${label} ===\nrejected: ${(err as Error).message}`);
  }
}

// 9. Interpreter terminates on finite trees of a self-recursive schema.
const interpResult = interpret(treeSchema, good);
show('interpreter on finite tree', { verdict: interpResult.verdict, steps: interpResult.steps });
