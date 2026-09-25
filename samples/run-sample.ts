/**
 * End-to-end sample: a document-export policy whose allow verdicts carry
 * limits (with dependencies), masked fields, audit tags, ordered pipeline
 * steps and output-format choices.
 *
 * Run with: node samples/run-sample.ts   (Node >= 22.18, no dependencies)
 */

import { evaluate, type Rule, type Decision } from '../src/index.ts';

const rules: Rule[] = [
  {
    id: 'r-analyst',
    when: { op: 'in', attr: 'role', values: ['analyst', 'auditor'] },
    effect: 'allow',
    obligations: [
      {
        kind: 'limit', id: 'ob-per-tx', name: 'perTxAmount', bound: 'upper', value: 8000,
        requires: [{ left: 'perTxAmount', rel: '<=', right: 'dailyAmount' }],
      },
      { kind: 'mask', id: 'ob-mask-pii', fields: ['ssn', 'dob'] },
      { kind: 'auditTags', id: 'ob-audit-export', tags: ['export'] },
      { kind: 'orderedSteps', id: 'ob-pipe', steps: ['redact', 'watermark', 'sign'] },
      { kind: 'outputFormat', id: 'ob-fmt-analyst', allowed: ['csv', 'json'] },
    ],
  },
  {
    id: 'r-finance',
    when: { op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 },
    effect: 'allow',
    obligations: [
      { kind: 'limit', id: 'ob-daily', name: 'dailyAmount', bound: 'upper', value: 5000 },
      { kind: 'mask', id: 'ob-mask-iban', fields: ['iban'] },
      { kind: 'auditTags', id: 'ob-audit-finance', tags: ['finance-review'] },
      { kind: 'outputFormat', id: 'ob-fmt-finance', allowed: ['json', 'parquet'] },
    ],
  },
  {
    id: 'r-eu',
    when: { op: 'in', attr: 'region', values: ['eu'] },
    effect: 'allow',
    obligations: [
      { kind: 'mask', id: 'ob-mask-gdpr', fields: ['address'] },
      { kind: 'auditTags', id: 'ob-audit-gdpr', tags: ['gdpr'] },
    ],
  },
  {
    id: 'r-emergency',
    when: { op: 'eq', attr: 'emergency', value: true },
    effect: 'allow',
    suppresses: ['auditTags'],
    reason: 'emergency export skips audit tagging',
  },
  {
    id: 'r-deny-huge',
    when: { op: 'intCmp', attr: 'amount', cmp: '>', value: 50000 },
    effect: 'deny',
    reason: 'amount exceeds hard ceiling',
  },
];

function show(title: string, decision: Decision): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(decision, null, 2));
}

// 1. Ordinary allow: obligations merged with per-kind algebras.
show(
  'allow: limits/mask/tags/steps/format merged',
  evaluate(rules, { role: 'analyst', amount: 4200, region: 'eu', emergency: false }),
);

// 2. Emergency: audit tags suppressed (recorded, not dropped).
show(
  'emergency: auditTags suppressed explicitly',
  evaluate(rules, { role: 'analyst', amount: 4200, region: 'eu', emergency: true }),
);

// 3. Withdraw the emergency rule: suppressed obligations are restored.
show(
  'emergency + r-emergency withdrawn: auditTags restored',
  evaluate(
    rules,
    { role: 'analyst', amount: 4200, region: 'eu', emergency: true },
    { withdrawn: ['r-emergency'] },
  ),
);

// 4. Deny: allow obligations are not executed.
show(
  'deny: allow obligations not executed',
  evaluate(rules, { role: 'analyst', amount: 60000, region: 'eu', emergency: false }),
);

// 5. Conflict: two mutually exclusive output formats -> explicit failure + minimal core.
const conflicting: Rule[] = [
  {
    id: 'r-csv', when: { op: 'in', attr: 'role', values: ['analyst'] }, effect: 'allow',
    obligations: [{ kind: 'outputFormat', id: 'ob-csv', allowed: ['csv'] }],
  },
  {
    id: 'r-xlsx', when: { op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 }, effect: 'allow',
    obligations: [{ kind: 'outputFormat', id: 'ob-xlsx', allowed: ['xlsx'] }],
  },
];
show(
  'conflict: mutually exclusive formats fail with minimal core',
  evaluate(conflicting, { role: 'analyst', amount: 4200 }),
);

// 6. Conflict: unsatisfiable limits -> explicit failure + minimal core.
const badLimits: Rule[] = [
  {
    id: 'r-floor', when: { op: 'in', attr: 'role', values: ['analyst'] }, effect: 'allow',
    obligations: [{ kind: 'limit', id: 'ob-floor', name: 'x', bound: 'lower', value: 10 }],
  },
  {
    id: 'r-cap', when: { op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 }, effect: 'allow',
    obligations: [{ kind: 'limit', id: 'ob-cap', name: 'x', bound: 'upper', value: 5 }],
  },
];
show(
  'conflict: unsatisfiable limits fail with minimal core',
  evaluate(badLimits, { role: 'analyst', amount: 4200 }),
);
