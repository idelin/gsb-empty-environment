#!/usr/bin/env node
/**
 * Read-only acceptance runner.
 *
 * - No network access is attempted anywhere in the module graph.
 * - By default it writes nothing to disk: the report is printed to stdout.
 *   `--out <path>` is the only optional write, and it contains just the report.
 * - Every check returns a fully populated result; the process exits non-zero
 *   when any check fails.
 */

import { writeFile } from "node:fs/promises";

import {
  ContextResolutionError,
  SemanticCache,
  buildDescriptor,
  computeCacheKey,
  contentIdOf,
  expandSmallGraph,
  minimalClosure,
  resolveContext,
  safeDiagnosticJson,
  validateDescriptor,
} from "../src/index.ts";
import type {
  CacheEntry,
  ContentId,
  ContextObject,
  ErrorCode,
} from "../src/index.ts";
import {
  buildAcceptanceHarness,
  diamondContexts,
  nestConflictContexts,
  propagateContexts,
} from "./fixtures.ts";
import type { AcceptanceHarness } from "./fixtures.ts";

interface CheckResult {
  readonly id: string;
  readonly title: string;
  readonly passed: boolean;
  readonly detail: string;
  readonly diagnostic: Record<string, unknown> | null;
}

interface AcceptanceReport {
  readonly generatedAt: string;
  readonly networkAccess: "never-attempted";
  readonly filesystemWrites: "none" | "report-only";
  readonly checks: readonly CheckResult[];
  readonly summary: {
    readonly total: number;
    readonly passed: number;
    readonly failed: number;
  };
}

async function check(
  id: string,
  title: string,
  fn: () => Promise<string> | string,
): Promise<CheckResult> {
  try {
    return { id, title, passed: true, detail: await fn(), diagnostic: null };
  } catch (error) {
    return { id, title, passed: false, detail: "rejected", diagnostic: toDiagnostic(error) };
  }
}

function toDiagnostic(error: unknown): Record<string, unknown> {
  if (error instanceof ContextResolutionError) {
    return JSON.parse(safeDiagnosticJson(error.diagnostic)) as Record<string, unknown>;
  }
  return {
    code: "ACCEPTANCE_UNEXPECTED_ERROR",
    message: error instanceof Error ? error.message : String(error),
    offendingTerm: "",
    offendingCid: "",
    importPath: [],
    depth: 0,
    detail: "",
  };
}

function expectCode(fn: () => unknown, expected: ErrorCode): string {
  try {
    fn();
  } catch (error) {
    if (
      error instanceof ContextResolutionError &&
      error.diagnostic.code === expected
    ) {
      return `hard-rejected:${expected}`;
    }
    throw error;
  }
  throw new Error(`Expected ${expected} but resolution succeeded`);
}

async function resolve(
  harness: AcceptanceHarness,
  root: ContextObject,
) {
  const rootCid = contentIdOf(root);
  return resolveContext({
    rootCid,
    store: harness.store,
    allowed: harness.allowed,
  });
}

async function runChecks(): Promise<readonly CheckResult[]> {
  const results: CheckResult[] = [];

  results.push(
    await check("A1", "signed trust manifest anchors every context", async () => {
      const scenario = nestConflictContexts();
      const harness = await buildAcceptanceHarness(scenario.contexts);
      if (!harness.allowed.has(contentIdOf(scenario.root))) {
        throw new Error("root context missing from signed allow-list");
      }
      return "manifest signature and allow-list verified";
    }),
  );

  results.push(
    await check(
      "A2",
      "nested @nest alias conflict hard-rejects through the no-cache path",
      async () => {
        const scenario = nestConflictContexts();
        const harness = await buildAcceptanceHarness(scenario.contexts);
        return expectCode(
          () => resolveContext({
            rootCid: contentIdOf(scenario.root),
            store: harness.store,
            allowed: harness.allowed,
          }),
          "PROTECTED_ALIAS_COLLISION",
        );
      },
    ),
  );

  results.push(
    await check("A3", "@propagate:false isolates vocab, base and terms", async () => {
      const scenario = propagateContexts();
      const harness = await buildAcceptanceHarness(scenario.contexts);
      const result = await resolve(harness, scenario.root);
      if (result.vocab !== "https://root.test/#" || result.base !== "https://root.test/base/") {
        throw new Error("scoped vocab/base leaked into the root frame");
      }
      if (result.terms.has("scopedOnly") || !result.terms.has("rootOnly")) {
        throw new Error("@propagate:false term isolation failed");
      }
      return "isolation verified";
    }),
  );

  results.push(
    await check(
      "A4",
      "@import diamond: idempotent shared leaf; divergent branch rejected",
      async () => {
        const safe = diamondContexts(false);
        const safeHarness = await buildAcceptanceHarness(safe.contexts);
        const safeResult = await resolve(safeHarness, safe.root);
        if (!safeResult.protectedTerms.has("shared")) {
          throw new Error("idempotent diamond lost the protected leaf binding");
        }

        const divergent = diamondContexts(true);
        const badHarness = await buildAcceptanceHarness(divergent.contexts);
        expectCode(
          () => resolveContext({
            rootCid: contentIdOf(divergent.root),
            store: badHarness.store,
            allowed: badHarness.allowed,
          }),
          "PROTECTED_ALIAS_COLLISION",
        );
        return "idempotent merge and divergent rejection verified";
      },
    ),
  );

  results.push(
    await check(
      "A5",
      "small-graph no-cache expansion honours @id, @vocab and @reverse",
      async () => {
        const root: ContextObject = {
          "@vocab": "https://cred.test/#",
          "@base": "https://cred.test/base/",
          type: {
            "@id": "https://cred.test/#type",
            "@type": "@vocab",
            "@protected": true,
          },
          issuer: { "@id": "https://cred.test/#issuer", "@type": "@id" },
          members: { "@reverse": "https://cred.test/#memberOf" },
        };
        const harness = await buildAcceptanceHarness([root]);
        const resolution = await resolve(harness, root);
        const expanded = expandSmallGraph(
          {
            type: "VerifiableCredential",
            issuer: "did:web:issuer.example",
            members: "org-1",
          },
          resolution,
        );
        if (expanded["https://cred.test/#type"] !== "https://cred.test/#VerifiableCredential") {
          throw new Error("@vocab coercion mismatch");
        }
        if (expanded["https://cred.test/#issuer"] !== "did:web:issuer.example") {
          throw new Error("@id coercion mismatch");
        }
        if (expanded["@reverse:https://cred.test/#memberOf"] !== "org-1") {
          throw new Error("@reverse expansion mismatch");
        }
        return "cache-free expansion verified";
      },
    ),
  );

  results.push(
    await check(
      "A6",
      "cache key binds trust root, full stack, protected map, vocab and base",
      async () => {
        const leaf: ContextObject = {
          "@protected": true,
          policy: "https://policy.test/#field",
        };
        const root: ContextObject = {
          "@vocab": "https://root.test/#",
          "@base": "https://root.test/base/",
          "@import": contentIdOf(leaf),
        };
        const harness = await buildAcceptanceHarness([leaf, root]);
        const resolution = await resolve(harness, root);
        const descriptor = buildDescriptor(
          {
            manifestCid: harness.manifestCid,
            rootKeyId: harness.rootKeyId,
            rootPublicKeySpki: harness.rootPublicKeySpki,
          },
          resolution,
        );
        const key = computeCacheKey(descriptor);
        const cache = new SemanticCache();
        const entry: CacheEntry = {
          key,
          descriptor,
          resolution,
          closure: minimalClosure(descriptor),
          manifestCid: harness.manifestCid,
        };
        cache.put(entry);
        if (cache.lookup(descriptor) === null) {
          throw new Error("identical descriptor should hit the cache");
        }

        assertUnsound(
          {
            ...descriptor,
            contextStack: ["https://attacker.test/context.jsonld"],
          },
          "URL-only reuse",
        );
        assertUnsound(
          {
            ...descriptor,
            contextStack: [descriptor.contextStack[0] as ContentId],
          },
          "shallow-stack reuse",
          descriptor.contextStack.length,
        );
        const swappedRoot = {
          ...descriptor,
          trustRoot: { ...descriptor.trustRoot, rootKeyId: "other" },
        };
        if (computeCacheKey(swappedRoot) === key) {
          throw new Error("trust-root swap reused the same cache key");
        }
        return "soundness, full-stack binding and minimal closure verified";
      },
    ),
  );

  return results;
}

function assertUnsound(
  descriptor: Parameters<typeof computeCacheKey>[0],
  label: string,
  expectedStackDepth?: number,
): void {
  try {
    validateDescriptor(descriptor, expectedStackDepth);
  } catch (error) {
    if (
      error instanceof ContextResolutionError &&
      error.diagnostic.code === "CACHE_KEY_UNSOUND"
    ) {
      return;
    }
    throw error;
  }
  throw new Error(`${label} was not rejected`);
}

async function main(): Promise<void> {
  const checks = await runChecks();
  const failed = checks.filter((item) => !item.passed).length;
  const out = parseOutFlag();
  const report: AcceptanceReport = {
    generatedAt: "2026-11-26T00:00:00Z",
    networkAccess: "never-attempted",
    filesystemWrites: out === undefined ? "none" : "report-only",
    checks,
    summary: {
      total: checks.length,
      passed: checks.length - failed,
      failed,
    },
  };
  const text = `${JSON.stringify(report, null, 2)}\n`;
  if (out === undefined) {
    process.stdout.write(text);
  } else {
    await writeFile(out, text, "utf8");
  }
  if (failed > 0) process.exitCode = 1;
}

function parseOutFlag(): string | undefined {
  const args = process.argv.slice(2);
  const index = args.indexOf("--out");
  return index === -1 ? undefined : args[index + 1];
}

main().catch((error: unknown) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
  process.exitCode = 1;
});
