# Cross-Origin Isolation & XS-Leak Policy Toolkit

Offline, zero-dependency TypeScript package for a Web security gateway host.
It adjudicates the browser-side and server-side cross-origin policy stack and
searches a multi-origin deployment for the shortest sequences that break
cross-origin isolation or enable XS-Leaks.

No network access is performed at any point; every decision is produced by
deterministic spec-modeling engines.

## Components

### 1. Browser isolation model (`src/coop.ts`, `src/coep.ts`, `src/origin.ts`)

- `BrowsingContextGroupMachine` — stateful COOP transition machine modeling
  the HTML "cross-origin opener policies" algorithms. It decides whether a
  top-level navigation allocates a fresh Browsing Context Group, whether a
  popup retains a live `window.opener`, and applies the sandbox override
  (sandboxed documents without `allow-popups-to-escape-sandbox` are forced to
  `unsafe-none` and an opaque origin).
- COOP values: `unsafe-none`, `same-origin-allow-popups`, `same-origin`,
  including the popup virtual-policy exception (`allow-popups` opener keeps
  an `unsafe-none` popup) and diagonal-policy severing.
- `evaluateSubresource` / `evaluateRedirectChain` — COEP
  (`unsafe-none` / `require-corp` / `credentialless`) evaluation of
  subresources and **every redirect hop**, including explicit opaque
  (`Origin: null`) hops from sandboxed iframe redirect chains.
- CORP (`same-origin` / `same-site` / `cross-origin`) matching per the Fetch
  "cross-origin resource policy check", anonymous CORS exemption under
  `require-corp`, and credential stripping under `credentialless`.
- `evaluateFrameEmbed` — iframe navigation COEP check plus the child's
  resulting `crossOriginIsolated` / credentialless-embedded state.
- `evaluateCrossOriginIsolated` — `self.crossOriginIsolated` for top-level
  and nested documents.
- Normalized origin/site primitives with a small built-in public-suffix list
  (extensible via `setPublicSuffixes`) and constant-structure allowlist
  comparison that defeats suffix/prefix lookalike origins.

### 2. Server-side gate (`src/fetch-metadata.ts`, `src/cors.ts`)

- `evaluateFetchMetadata` — Sec-Fetch-Site/Mode/Dest/User resource isolation
  policy: same-origin always allowed, user-activated top-level navigations
  allowed narrowly, cross-site CSRF / subresource probes / iframe embedding /
  WebSocket handshakes denied, and `Origin: null` privilege escalation
  refused. Declared metadata is cross-checked against the actual `Origin`.
- `validateCorsResponse` / `validatePreflight` — hard rejection of
  `Access-Control-Allow-Origin: *` together with
  `Access-Control-Allow-Credentials: true`, normalized tuple comparison for
  reflected origins, opaque `null` reflection refusal, `Vary: Origin`
  enforcement, and preflight `Allow-Headers: *` / `Authorization` rules.

### 3. Bounded breach searcher (`src/searcher.ts`)

`searchIsolationBreaches(endpoints, bounds)` performs breadth-first search
over navigation, popup, iframe, subresource, CORS, preflight, redirect and
cross-site POST actions. It reports each policy failure with:

- severity and violated security goal;
- the **shortest** action sequence demonstrating the breach;
- raw engine evidence and spec references.

The search is bounded by `maxDepth`, `maxStates` and
`maxFindingsPerKind`, and reuses the same engines as the online gate.

### 4. Executable spec matrix (`src/scenario-matrix.ts`)

38 normative cells across COOP, COOP+COEP isolation, COEP/CORP subresources,
frame embedding, CORS, Fetch Metadata and `Origin: null` redirects.
`runScenarioMatrix()` evaluates every cell; `matrixSummary()` groups results
for CI gating.

## Usage

```ts
import {
  BrowsingContextGroupMachine,
  evaluateFetchMetadata,
  evaluateSubresource,
  parseCoep,
  parseCoop,
  searchIsolationBreaches,
  validateCorsResponse,
} from './src/index.ts';

const machine = new BrowsingContextGroupMachine({
  origin: 'https://victim.example',
  coop: parseCoop({ 'cross-origin-opener-policy': 'same-origin' }),
});
const { decision } = machine.openPopup({
  responseOrigin: 'https://attacker.test',
  coop: parseCoop({}),
});
// decision.openerRetained === false, fresh BCG allocated

const verdict = evaluateSubresource(
  {
    embedderOrigin: 'https://victim.example',
    requestOrigin: 'https://attacker.test',
    mode: 'no-cors',
    destination: 'script',
    hops: [{ url: 'https://attacker.test' }],
  },
  parseCoep({ 'cross-origin-embedder-policy': 'require-corp' }),
);
// verdict.allowed === false, reason === 'corp-missing'
```

## Scripts

- `npm test` — 65 Node built-in test runner cases (all engines + searcher).
- `npm run matrix` — execute the 38-cell normative scenario matrix.
- `npm run demo` — end-to-end walkthrough against a sample deployment.

Requires Node.js >= 22.6 with `--experimental-strip-types` (validated on
Node 23). No packages are installed and no network is required.
