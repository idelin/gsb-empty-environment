import {
  BatchReleasedError,
  BatchSource,
  BatchSpec,
  ColumnBatch,
  LoadCancelledError,
  Plan,
  Value,
  WideLoader,
  columnData,
  execute,
  filterBatch,
  runReference,
} from '../src/index';

function wideColumn(values: Value[], delayMs: number, onLoad?: () => void): WideLoader {
  return ({ signal }) =>
    new Promise((resolve, reject) => {
      if (onLoad) onLoad();
      const timer = setTimeout(() => resolve(columnData(values)), delayMs);
      if (signal) {
        signal.addEventListener('abort', () => {
          clearTimeout(timer);
          reject(new LoadCancelledError('loader aborted'));
        });
      }
    });
}

function demoSpecs(counter?: { payload: number; trace: number }): BatchSpec[] {
  const payload = (values: Value[], delay: number): WideLoader =>
    wideColumn(values, delay, counter ? () => counter.payload++ : undefined);
  const trace = (values: Value[]): WideLoader =>
    wideColumn(values, 5, counter ? () => counter.trace++ : undefined);
  return [
    {
      version: 1,
      rowIds: [101, 102, 103, 104],
      rowVersions: [1, 1, 1, 1],
      narrow: {
        region: { values: ['cn', 'us', 'cn', null] },
        status: { values: [200, 500, null, 200] },
      },
      wide: {
        payload: payload(['alpha', 'beta', 'gamma', 'delta'], 20),
        trace: trace(['t1', 't2', 't3', 't4']),
      },
    },
    {
      version: 1,
      rowIds: [105, 106, 107],
      rowVersions: [1, 2, 1],
      narrow: {
        region: { values: ['us', 'cn', 'us'] },
        status: { values: [200, 200, 404] },
      },
      wide: {
        payload: payload(['epsilon', 'zeta', 'eta'], 10),
        trace: trace(['t5', 't6', 't7']),
      },
    },
    {
      version: 2,
      rowIds: [105, 108],
      rowVersions: [3, 1],
      narrow: {
        region: { values: ['cn', 'us'] },
        status: { values: [200, 200] },
      },
      wide: {
        payload: payload(['theta', 'iota'], 15),
        trace: trace(['t8', 't9']),
      },
    },
  ];
}

async function main(): Promise<void> {
  console.log('=== 1. filter + stable sort + project (late materialization) ===');
  const counter = { payload: 0, trace: 0 };
  const source = new BatchSource(demoSpecs(counter), 3);
  const plan: Plan = {
    filter: { col: 'status', op: 'eq', value: 200 },
    sort: { key: 'region' },
    project: ['region', 'status', 'payload'],
  };
  const result = await execute(source, plan);
  console.log('columns:', result.columns.join(', '));
  for (const r of result.rows) {
    console.log(`  rowId=${r.rowId} v${r.rowVersion} -> ${JSON.stringify(r.values)}`);
  }
  console.log('stats:', JSON.stringify(source.stats.snapshot(), null, 2));
  console.log(`wide loads: payload=${counter.payload}, trace=${counter.trace} (trace never projected -> never read)`);

  console.log('\n=== 2. same plan through the eager row-by-row reference ===');
  const reference = await runReference(demoSpecs(), plan);
  const identical = JSON.stringify(result) === JSON.stringify(reference);
  console.log('pipeline output identical to reference:', identical);

  console.log('\n=== 3. cancellable wide-column load ===');
  const slow = new BatchSource(demoSpecs(), 3);
  const ac = new AbortController();
  const pending = execute(slow, { project: ['payload'] }, { signal: ac.signal });
  setTimeout(() => ac.abort(), 5);
  try {
    await pending;
    console.log('unexpected: query completed');
  } catch (err) {
    console.log(`query cancelled as expected: ${(err as Error).name}`);
    console.log('load stats:', JSON.stringify((slow.stats.loads.get('payload') || {})));
  }

  console.log('\n=== 4. buffer reuse invalidates old views ===');
  const reused = new BatchSource(demoSpecs(), 1);
  const it = reused.batches();
  const b1 = (await it.next()).value as ColumnBatch;
  const staleView = b1.narrowView('region');
  const staleSel = filterBatch(b1, { col: 'status', op: 'eq', value: 200 });
  await it.next();
  try {
    staleView.valueAt(0);
  } catch (err) {
    console.log(`old view rejected: ${(err as Error).name}: ${(err as Error).message}`);
  }
  try {
    staleSel.resolve();
  } catch (err) {
    console.log(`old selection rejected: ${(err as Error).name}`);
    if (!(err instanceof BatchReleasedError)) throw err;
  }
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
