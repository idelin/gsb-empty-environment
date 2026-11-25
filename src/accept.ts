import { Authority } from './authority.ts';
import { MergeEngine } from './engine.ts';
import { canonicalJson, shortHash, sortErrors } from './locations.ts';
import type {
  AcceptanceError,
  AcceptanceResult,
  MergeInput,
  PublishHost,
  Publication,
} from './types.ts';

/**
 * Pure merge acceptance. Never throws for policy failures: it returns a
 * deterministic `{ ok:false, errors }` rejection. Unexpected malformed input is
 * reported as an `INVALID_INPUT`/`INTERNAL` error rather than escaping.
 */
export function acceptMerge(input: MergeInput): AcceptanceResult {
  try {
    const preflight = preflightNodeIds(input);
    const authority = new Authority(input);
    const engine = new MergeEngine(input, authority);
    const outcome = engine.run();

    const errors = sortErrors([...preflight, ...outcome.errors]);
    if (errors.length > 0) {
      return { ok: false, errors };
    }

    const leaves = [...outcome.leaves].sort((a, b) => a.pointer.localeCompare(b.pointer));
    const provenance = [...outcome.provenance].sort((a, b) => a.pointer.localeCompare(b.pointer));
    const seed = input.options?.publicationSeed ?? '';
    const publicationId = `pub_${shortHash(`${seed}:${canonicalJson(outcome.tree)}:${canonicalJson(
      leaves.map((leaf) => leaf.pointer),
    )}`)}`;

    const publication: Publication = {
      ok: true,
      publicationId,
      tree: outcome.tree,
      leaves,
      provenance,
      trace: outcome.trace,
    };
    return publication;
  } catch (error) {
    return {
      ok: false,
      errors: sortErrors([
        {
          code: 'INTERNAL',
          message: error instanceof Error ? error.message : String(error),
          location: { pointer: '' },
        },
      ]),
    };
  }
}

/**
 * Accept then publish. The host sink is invoked ONLY after full acceptance,
 * giving the "failure = zero publication" guarantee.
 */
export async function publishAccepted(
  host: PublishHost,
  input: MergeInput,
): Promise<AcceptanceResult> {
  const result = acceptMerge(input);
  if (result.ok) {
    await host.publish(result);
  }
  return result;
}

interface FingerprintedDoc {
  docId: string;
  nodes: MergeInput['branches'][number]['doc']['nodes'];
}

function preflightNodeIds(input: MergeInput) {
  const errors: AcceptanceError[] = [];
  const docs: FingerprintedDoc[] = input.branches.map((branch) => ({
    docId: branch.doc.docId,
    nodes: branch.doc.nodes,
  }));
  for (const tree of input.documents ?? []) {
    docs.push({ docId: tree.docId, nodes: tree.nodes });
  }
  for (const tree of Object.values(input.templates ?? {})) {
    docs.push({ docId: tree.docId, nodes: tree.nodes });
  }

  const fingerprints = new Map<string, { docId: string; fingerprint: string }>();
  for (const { docId, nodes } of docs) {
    for (const [id, node] of Object.entries(nodes)) {
      const fingerprint = nodeFingerprint(node);
      const first = fingerprints.get(id);
      if (first && first.fingerprint !== fingerprint) {
        errors.push({
          code: 'NODE_ID_CONTENT_CONFLICT' as const,
          message: `Immutable node id "${id}" has conflicting definitions in "${first.docId}" and "${docId}"`,
          location: { pointer: '', nodeId: id },
          detail: { firstDoc: first.docId, secondDoc: docId },
        });
      } else if (!first) {
        fingerprints.set(id, { docId, fingerprint });
      }
    }
  }
  return errors;
}

function nodeFingerprint(node: {
  kind: string;
  value?: unknown;
  template?: string;
}): string {
  // Only intrinsic terminal content is immutable for an id. Object edge lists
  // are name/parentage decisions that may evolve across branches/overlays, so
  // they are excluded and authorised as separate facets.
  return JSON.stringify({
    k: node.kind,
    ...(node.value !== undefined ? { v: node.value } : {}),
    ...(node.template !== undefined ? { t: node.template } : {}),
  });
}
