/**
 * End-to-end walkthrough of the three package components against a small
 * multi-origin deployment. Run with `npm run demo` (fully offline).
 */

import {
  BrowsingContextGroupMachine,
  evaluateFetchMetadata,
  evaluateSubresource,
  parseCoep,
  parseCoop,
  searchIsolationBreaches,
  validateCorsResponse,
  type EndpointConfig,
} from '../src/index.ts';

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

section('1. Browsing Context Group state machine (COOP)');
const victimWindow = new BrowsingContextGroupMachine({
  origin: VICTIM,
  coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
});
const popup = victimWindow.openPopup({
  responseOrigin: ATTACKER,
  coop: parseCoop({ 'cross-origin-opener-policy': null }),
});
console.log(
  `strict COOP popup -> new BCG=${popup.decision.switchesBrowsingContextGroup}, ` +
    `opener retained=${popup.decision.openerRetained}`,
);

section('2. COEP/CORP subresource adjudication');
const blocked = evaluateSubresource(
  {
    embedderOrigin: VICTIM,
    requestOrigin: ATTACKER,
    mode: 'no-cors',
    destination: 'script',
    hops: [{ url: ATTACKER }],
  },
  parseCoep({ 'cross-origin-embedder-policy': 'require-corp' }),
);
console.log(`CORP-less cross-origin script: ${blocked.details}`);

const allowed = evaluateSubresource(
  {
    embedderOrigin: VICTIM,
    requestOrigin: ATTACKER,
    mode: 'no-cors',
    destination: 'image',
    hops: [{ url: ATTACKER, corp: 'cross-origin' }],
  },
  parseCoep({ 'cross-origin-embedder-policy': 'require-corp' }),
);
console.log(`CORP cross-origin image: allowed=${allowed.allowed}`);

section('3. CORS hardening');
const corsVerdict = validateCorsResponse(
  ATTACKER,
  {
    'access-control-allow-origin': '*',
    'access-control-allow-credentials': 'true',
  },
  { allowedOrigins: [ATTACKER], allowCredentials: true },
);
console.log(
  `wildcard+credentials allowed=${corsVerdict.allowed} ` +
    `violations=${corsVerdict.violations.map((v) => v.code).join(',')}`,
);

section('4. Fetch Metadata gate');
const metadataVerdict = evaluateFetchMetadata(
  {
    method: 'POST',
    headers: {
      'sec-fetch-site': 'cross-site',
      'sec-fetch-mode': 'cors',
      'sec-fetch-dest': 'empty',
      origin: ATTACKER,
    },
  },
  { serverOrigin: VICTIM },
);
console.log(
  `cross-site POST -> allow=${metadataVerdict.allow} code=${metadataVerdict.code}`,
);

section('5. Bounded XS-Leak / isolation-breach search');
const endpoints: EndpointConfig[] = [
  {
    id: 'victim',
    origin: VICTIM,
    coop: 'same-origin',
    coep: 'require-corp',
    goals: [
      'cross-origin-isolated',
      'opener-protected',
      'credential-protected',
      'csrf-protected',
    ],
  },
  {
    id: 'attacker',
    origin: ATTACKER,
    sandbox: 'allow-scripts',
    cors: { mode: 'wildcard', allowCredentials: true },
    metadataGate: { serverOrigin: VICTIM },
    goals: ['credential-protected', 'csrf-protected'],
  },
];

const result = searchIsolationBreaches(endpoints, { maxDepth: 2 });
console.log(
  `expanded=${result.statesExpanded} pruned=${result.statesPruned} ` +
    `depth=${result.depthReached} findings=${result.findings.length}`,
);
for (const finding of result.findings) {
  const sequence = finding.sequence
    .map((step) => `${step.kind}>${step.target}`)
    .join(' -> ');
  console.log(
    `[${finding.severity.padEnd(8)}] ${finding.violatedGoal.padEnd(22)} ` +
      `${finding.title} (${sequence})`,
  );
}
