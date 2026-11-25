import { buildSenderReceipts, coveringChainsFor, evaluateBatch } from "./evaluate.ts";
import {
  materializeAll,
  verifyAlias,
  verifyConsent,
  type IngestedInput,
} from "./engine-common.ts";
import { buildAliasDirectory, type AliasDirectoryView } from "./alias.ts";
import {
  materializeChain,
  type MaterializedChain,
} from "./materialize.ts";
import type {
  AuthorizationReader,
  BatchEvaluation,
  BatchItem,
  ChainId,
  CoveringChain,
  CoverageQuery,
  KeyDirectory,
  PurposeCatalog,
  RegionCatalog,
  SenderReceipt,
  SignedAliasAssertion,
  SignedConsentStatement,
} from "./types.ts";

export interface IncrementalCacheStats {
  /** Number of statement ingest calls (including reordered arrivals). */
  ingestCalls: number;
  /** Number of chains actually rematerialized since the previous read. */
  chainRematerializations: number;
  /** Number of alias-directory rebuilds. */
  aliasRebuilds: number;
}

/**
 * Incremental interpreter. Raw envelopes per chain are retained, while the
 * derived {@link MaterializedChain} view is cached. A selective revocation or
 * any later statement only invalidates the chain it belongs to; untouched
 * chains keep serving from cache. Corrections/forks likewise trigger a scoped
 * rebuild of exactly the affected chain.
 */
export class IncrementalAuthorizationEngine implements AuthorizationReader {
  private readonly rawByChain = new Map<ChainId, SignedConsentStatement[]>();
  private aliasAssertions: SignedAliasAssertion[] = [];
  private readonly cache = new Map<ChainId, MaterializedChain>();
  private aliasCache: AliasDirectoryView | undefined;
  private chainListCache: MaterializedChain[] | undefined;
  private readonly keyDirectory: KeyDirectory;
  private readonly purposes: PurposeCatalog;
  private readonly regions: RegionCatalog;
  private stats: IncrementalCacheStats = {
    ingestCalls: 0,
    chainRematerializations: 0,
    aliasRebuilds: 0,
  };

  constructor(
    keyDirectory: KeyDirectory,
    purposes: PurposeCatalog,
    regions: RegionCatalog,
    initial?: IngestedInput,
  ) {
    this.keyDirectory = keyDirectory;
    this.purposes = purposes;
    this.regions = regions;
    if (initial) {
      this.ingestStatements(initial.statements);
      this.ingestAliases(initial.aliases);
    }
  }

  /** Ingest one or more signed statements in any arrival order. */
  ingestStatements(envelopes: readonly SignedConsentStatement[]): void {
    if (envelopes.length === 0) {
      return;
    }
    this.stats.ingestCalls += 1;
    const touched = new Set<ChainId>();
    for (const envelope of envelopes) {
      const chainId = envelope.statement.chainId;
      const group = this.rawByChain.get(chainId) ?? [];
      group.push(envelope);
      this.rawByChain.set(chainId, group);
      touched.add(chainId);
    }
    for (const chainId of touched) {
      this.cache.delete(chainId);
      if (this.chainListCache) {
        this.chainListCache = this.chainListCache.filter(
          (chain) => chain.chainId !== chainId,
        );
      }
    }
  }

  ingestAliases(assertions: readonly SignedAliasAssertion[]): void {
    if (assertions.length === 0) {
      return;
    }
    this.aliasAssertions.push(...assertions);
    this.aliasCache = undefined;
  }

  private ensureChain(chainId: ChainId): MaterializedChain {
    const cached = this.cache.get(chainId);
    if (cached) {
      return cached;
    }
    const group = this.rawByChain.get(chainId) ?? [];
    const materialized = materializeChain(
      chainId,
      group,
      verifyConsent(this.keyDirectory),
      this.purposes,
      this.regions,
    );
    this.cache.set(chainId, materialized);
    this.stats.chainRematerializations += 1;
    return materialized;
  }

  private ensureAliases(): AliasDirectoryView {
    if (!this.aliasCache) {
      this.aliasCache = buildAliasDirectory(
        this.aliasAssertions,
        verifyAlias(this.keyDirectory),
      );
      this.stats.aliasRebuilds += 1;
    }
    return this.aliasCache;
  }

  private index() {
    return {
      chains: () => {
        const chainIds = [...this.rawByChain.keys()].sort((a, b) =>
          a.localeCompare(b),
        );
        const listed = new Set(
          (this.chainListCache ?? []).map((chain) => chain.chainId),
        );
        for (const chainId of chainIds) {
          if (!listed.has(chainId)) {
            this.chainListCache = [
              ...(this.chainListCache ?? []),
              this.ensureChain(chainId),
            ].sort((a, b) => a.chainId.localeCompare(b.chainId));
          }
        }
        return this.chainListCache ?? [];
      },
    };
  }

  coveringChains(query: CoverageQuery): CoveringChain[] {
    return coveringChainsFor(
      this.index(),
      this.purposes,
      this.regions,
      query,
    );
  }

  evaluateBatch(items: readonly BatchItem[], at: number): BatchEvaluation {
    return evaluateBatch(
      this.index(),
      this.ensureAliases(),
      this.purposes,
      this.regions,
      items,
      at,
    );
  }

  senderReceipts(
    items: readonly BatchItem[],
    at: number,
  ): SenderReceipt[] {
    return buildSenderReceipts(items, this.evaluateBatch(items, at));
  }

  cacheStats(): IncrementalCacheStats {
    return { ...this.stats };
  }

  /**
   * Snapshot every currently cached/derived chain. Used by tests to prove the
   * incremental cache equals the full-rebuild reference at each instant.
   */
  snapshot(): MaterializedChain[] {
    return [...this.rawByChain.keys()]
      .sort((a, b) => a.localeCompare(b))
      .map((chainId) => this.ensureChain(chainId));
  }

  /** Convenience full rebuild used only by cross-check tests. */
  fullRebuildReference(): MaterializedChain[] {
    return materializeAll(
      {
        statements: [...this.rawByChain.values()].flat(),
        aliases: this.aliasAssertions,
      },
      this.keyDirectory,
      this.purposes,
      this.regions,
    ).chains;
  }

}
