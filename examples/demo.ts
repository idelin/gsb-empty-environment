import { and, checkOrderIndependence, eq, evaluate, gt, renderEvidence, Rule } from '../src';

const rules: Rule[] = [
  { id: 'vip-discount', priority: 10, condition: eq('vip', true), assigns: { discount: 0.2 } },
  { id: 'bulk-discount', priority: 5, condition: gt('items', 10), assigns: { discount: 0.1 } },
  { id: 'holiday-discount', priority: 10, condition: eq('holiday', true), assigns: { discount: 0.25 } },
  {
    id: 'express-shipping',
    priority: 3,
    condition: and(eq('vip', true), gt('items', 5)),
    assigns: { shipping: 'express' },
  },
];

const snapshot = { vip: true, holiday: true, items: 12 };

const result = evaluate(rules, snapshot);
console.log('=== output ===');
console.log(result.output);
console.log('\n=== evidence tree (per field) ===');
console.log(renderEvidence(result));

const report = checkOrderIndependence(rules, snapshot);
console.log('\n=== order independence ===');
console.log(`independent=${report.independent} permutationsChecked=${report.permutationsChecked}`);
