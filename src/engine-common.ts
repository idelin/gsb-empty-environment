import { verifyEnvelope } from "./signing.ts";
import { buildAliasDirectory, type AliasDirectoryView } from "./alias.ts";
import {
  materializeChain,
  type MaterializedChain,
} from "./materialize.ts";
import type {
  ChainId,
  KeyDirectory,
  PurposeCatalog,
  RegionCatalog,
  SignedAliasAssertion,
  SignedConsentStatement,
} from "./types.ts";

export interface IngestedInput {
  readonly statements: readonly SignedConsentStatement[];
  readonly aliases: readonly SignedAliasAssertion[];
}

export function groupByChain(
  envelopes: readonly SignedConsentStatement[],
): Map<ChainId, SignedConsentStatement[]> {
  const groups = new Map<ChainId, SignedConsentStatement[]>();
  for (const envelope of envelopes) {
    const chainId = envelope.statement.chainId;
    const group = groups.get(chainId) ?? [];
    group.push(envelope);
    groups.set(chainId, group);
  }
  return groups;
}

export function verifyConsent(
  directory: KeyDirectory,
): (envelope: SignedConsentStatement) => boolean {
  return (envelope) =>
    verifyEnvelope(envelope, directory, (statement) => statement.issuer);
}

export function verifyAlias(
  directory: KeyDirectory,
): (envelope: SignedAliasAssertion) => boolean {
  return (envelope) =>
    verifyEnvelope(envelope, directory, (statement) => statement.issuer);
}

export function materializeAll(
  input: IngestedInput,
  keyDirectory: KeyDirectory,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
): { chains: MaterializedChain[]; aliases: AliasDirectoryView } {
  const verifier = verifyConsent(keyDirectory);
  const chains = [...groupByChain(input.statements).entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([chainId, group]) =>
      materializeChain(chainId, group, verifier, purposes, regions),
    );
  const aliases = buildAliasDirectory(input.aliases, verifyAlias(keyDirectory));
  return { chains, aliases };
}
