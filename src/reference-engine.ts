import { buildSenderReceipts, coveringChainsFor, evaluateBatch } from "./evaluate.ts";
import { materializeAll, type IngestedInput } from "./engine-common.ts";
import type {
  AuthorizationReader,
  BatchEvaluation,
  BatchItem,
  CoveringChain,
  CoverageQuery,
  KeyDirectory,
  PurposeCatalog,
  RegionCatalog,
  SenderReceipt,
} from "./types.ts";

/**
 * Point-in-time full recomputation reference. Every query rebuilds the whole
 * authorization view from the raw signed inputs, so this engine has no cache
 * to go stale. It is the oracle the incremental engine is cross-checked
 * against.
 */
export class ReferenceAuthorizationEngine implements AuthorizationReader {
  private readonly input: IngestedInput;
  private readonly keyDirectory: KeyDirectory;
  private readonly purposes: PurposeCatalog;
  private readonly regions: RegionCatalog;

  constructor(
    input: IngestedInput,
    keyDirectory: KeyDirectory,
    purposes: PurposeCatalog,
    regions: RegionCatalog,
  ) {
    this.input = input;
    this.keyDirectory = keyDirectory;
    this.purposes = purposes;
    this.regions = regions;
  }

  private rebuild() {
    return materializeAll(
      this.input,
      this.keyDirectory,
      this.purposes,
      this.regions,
    );
  }

  coveringChains(query: CoverageQuery): CoveringChain[] {
    const { chains } = this.rebuild();
    return coveringChainsFor(
      { chains: () => chains },
      this.purposes,
      this.regions,
      query,
    );
  }

  evaluateBatch(items: readonly BatchItem[], at: number): BatchEvaluation {
    const { chains, aliases } = this.rebuild();
    return evaluateBatch(
      { chains: () => chains },
      aliases,
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
}
