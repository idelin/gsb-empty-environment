import { createHash } from 'crypto';
import { CapabilityDecl, PartyDeclaration } from './types';

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(canonicalize);
  }
  if (value !== null && typeof value === 'object') {
    const src = value as { [key: string]: unknown };
    const out: { [key: string]: unknown } = {};
    for (const key of Object.keys(src).sort()) {
      const v = src[key];
      if (v !== undefined) {
        out[key] = canonicalize(v);
      }
    }
    return out;
  }
  return value;
}

/** Deterministic JSON: object keys sorted recursively, undefined dropped. */
export function canonicalJson(value: unknown): string {
  return JSON.stringify(canonicalize(value));
}

export function sha256Hex(data: string): string {
  return createHash('sha256').update(data, 'utf8').digest('hex');
}

function canonicalCapability(cap: CapabilityDecl): unknown {
  return {
    name: cap.name,
    priority: cap.priority === undefined ? 1 : cap.priority,
    requires: (cap.requires || []).slice().sort(),
    conflicts: (cap.conflicts || []).slice().sort(),
    mandatory: cap.mandatory === true,
  };
}

/**
 * Canonical form of a declaration. Semantically irrelevant differences
 * (key order, capability order, constraint list order, omitted defaults)
 * collapse to the same canonical form, so the hash binds the declared
 * content itself.
 */
export function canonicalDeclaration(decl: PartyDeclaration): unknown {
  const caps = decl.capabilities
    .map(canonicalCapability)
    .sort((a, b) => {
      const an = (a as { name: string }).name;
      const bn = (b as { name: string }).name;
      return an < bn ? -1 : an > bn ? 1 : 0;
    });
  return {
    party: decl.party,
    round: decl.round,
    issuedAt: decl.issuedAt === undefined ? null : decl.issuedAt,
    capabilities: caps,
  };
}

export function hashDeclaration(decl: PartyDeclaration): string {
  return sha256Hex(canonicalJson(canonicalDeclaration(decl)));
}
