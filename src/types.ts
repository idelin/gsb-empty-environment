export interface CapabilityOffer {
  name: string;
  priority: number;
  requires?: string[];
  conflictsWith?: string[];
  mandatory?: boolean;
}

export interface Declaration {
  party: string;
  round: string;
  nonce: string;
  issuedAt: number;
  capabilities: CapabilityOffer[];
  digest: string;
}

export interface MissingOfferReason {
  kind: "missing-offer";
  capability: string;
  offeredBy: string[];
  requiredBy: string;
  path: string[];
}

export interface MutualExclusionReason {
  kind: "mutual-exclusion";
  capabilities: [string, string];
}

export type ConflictReason = MissingOfferReason | MutualExclusionReason;

export interface ConflictRequirement {
  party: string;
  capability: string;
}

export interface MinimalConflict {
  requirements: ConflictRequirement[];
  reasons: ConflictReason[];
}

export interface NegotiationResult {
  status: "AGREED" | "CONFLICT";
  round: string;
  parties: [string, string];
  config: string[] | null;
  score: number | null;
  conflict: MinimalConflict | null;
  declarationDigests: Record<string, string>;
  transcriptHash: string;
}
