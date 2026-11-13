import { decideMixedContent } from "./decision.ts";
import type {
  AssembleOptions,
  MixedContentDecision,
  UpgradeProbe,
} from "./types.ts";
import { resolveUrl } from "./url.ts";

/**
 * Default reachability probe: a HEAD request against the upgraded URL.
 * Any HTTP response means the secure origin answered (a 404 or 405 is still
 * a reachable server); only a network failure reports false.
 */
const defaultProbe: UpgradeProbe = async (url: URL): Promise<boolean> => {
  if (typeof globalThis.fetch !== "function") {
    throw new Error(
      "No global fetch available; pass an explicit probe to assembleRequest",
    );
  }
  try {
    await globalThis.fetch(url, { method: "HEAD", redirect: "follow" });
    return true;
  } catch {
    return false;
  }
};

/**
 * Run the mixed-content policy for one request and finalize its URL.
 *
 * For images whose URL was upgraded to https, the upgraded URL is probed
 * exactly once. If it is unreachable the image is reported as
 * `upgrade-failed` and the original http URL is never retried.
 */
export async function assembleRequest(
  options: AssembleOptions,
): Promise<MixedContentDecision> {
  const { probe = defaultProbe, ...requestInput } = options;
  const decision = decideMixedContent(requestInput);

  if (
    decision.allowed &&
    decision.upgraded &&
    decision.kind === "image"
  ) {
    let originalUrl: string;
    try {
      originalUrl = resolveUrl(
        options.resourceUrl,
        options.documentUrl,
      ).href;
    } catch {
      originalUrl = String(options.resourceUrl);
    }

    const reachable = await probe(decision.url);
    if (!reachable) {
      return {
        allowed: false,
        kind: decision.kind,
        reason: "upgrade-failed",
        originalUrl,
        upgradedUrl: decision.url,
      };
    }
  }

  return decision;
}
