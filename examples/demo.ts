// Runnable samples: node --experimental-strip-types examples/demo.ts
import {
  NonPSDError,
  buildSourceModel,
  propagateFirstOrder,
  propagateLinear,
  type PropagationReport,
} from '../src/index.ts';

function show(title: string, report: PropagationReport): void {
  console.log(`\n=== ${title} ===`);
  console.log('assumptions:');
  for (const a of report.assumptions) console.log(`  - ${a}`);
  for (const w of report.warnings) console.log(`  ! warning: ${w}`);
  console.log('exact coefficients:');
  for (const c of report.coefficients) {
    const terms = c.terms.map((t) => `${t.coefficient.toString()}*${t.input}`).join(' + ');
    console.log(`  ${c.output} = ${terms}`);
  }
  for (const o of report.outputs) {
    console.log(`  ${o.id}: value = ${o.value.toString()}`);
    console.log(`    ${o.varianceExpression}`);
    console.log(
      `    u(${o.id}) = ${o.std.decimal}  [sqrt rounded ${o.std.rounding} to ${o.std.places} places]`,
    );
  }
}

// 1) Independent linear: y = 2 x1 + 3 x2, u1 = 0.1, u2 = 0.2
show(
  'Independent linear propagation',
  propagateLinear({
    inputs: [
      { id: 'x1', value: '10' },
      { id: 'x2', value: '20' },
    ],
    outputs: [{ id: 'y', coefficients: [2, 3] }],
    covariance: [
      ['0.01', 0],
      [0, '0.04'],
    ],
  }),
);

// 2) Shared underlying error source -> full correlation (no RSS).
const model = buildSourceModel(
  [
    { id: 'x1', value: '10.0', contributions: [{ source: 'cal' }, { source: 'r1' }] },
    { id: 'x2', value: '20.0', contributions: [{ source: 'cal' }, { source: 'r2' }] },
  ],
  [
    { id: 'cal', std: '0.3' }, // shared calibration error
    { id: 'r1', std: '0.1' }, // independent readout noise
    { id: 'r2', std: '0.1' },
  ],
);
console.log('\n=== Covariance graph from shared sources ===');
console.log('edges:', JSON.stringify(model.edges));
console.log('Sigma:', JSON.stringify(model.covariance));
show(
  'Sum of two inputs sharing a calibration source',
  propagateLinear({
    inputs: [
      { id: 'x1', value: '10.0', contributions: [{ source: 'cal' }, { source: 'r1' }] },
      { id: 'x2', value: '20.0', contributions: [{ source: 'cal' }, { source: 'r2' }] },
    ],
    outputs: [{ id: 'sum', coefficients: [1, 1] }],
    sources: [
      { id: 'cal', std: '0.3' },
      { id: 'r1', std: '0.1' },
      { id: 'r2', std: '0.1' },
    ],
  }),
);

// 3) Nonlinear (first-order only, explicitly declared): P = V * I at (2, 5).
show(
  'Nonlinear first-order: P = V*I',
  propagateFirstOrder({
    inputs: [
      { id: 'V', value: '2' },
      { id: 'I', value: '5' },
    ],
    outputs: [{ id: 'P', valueAtPoint: '10', derivatives: ['5', '2'] }],
    covariance: [
      ['0.01', 0],
      [0, '0.04'],
    ],
    firstOrderDeclared: true,
    validityNote: '工作点 (2, 5) 附近小信号范围',
  }),
);

// 4) Contradictory covariance is rejected with negative-direction evidence.
console.log('\n=== Contradictory covariance (|rho| > 1) ===');
try {
  propagateLinear({
    inputs: [
      { id: 'x1', value: 0 },
      { id: 'x2', value: 0 },
    ],
    outputs: [{ id: 'y', coefficients: [1, 1] }],
    covariance: [
      [1, 2],
      [2, 1],
    ],
  });
} catch (e) {
  if (e instanceof NonPSDError) {
    console.log(`rejected: ${e.message}`);
  } else {
    throw e;
  }
}
