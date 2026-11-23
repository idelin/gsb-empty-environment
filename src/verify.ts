import type { Grammar, InclusionCertificate } from './types.ts';
import { assemblyRefSubset } from './version.ts';
import { canonicalHash, RULE_VERSION } from './inclusion.ts';

export interface VerificationResult {
  valid: boolean;
  reason?: string;
}

/**
 * Independent certificate verification. The verifier trusts nothing but the
 * certificate's raw proof edges and replays the containment rules itself:
 *
 *  1. hashes of both grammars must match the certificate;
 *  2. the rule version must be the one this verifier implements;
 *  3. for every certified pair, each inner alternative must name an outer
 *     alternative with a contained head, equal arity and equal variance, and
 *     every argument must link to another pair with the right orientation;
 *  4. the start pair must be present;
 *  5. a `subset` verdict requires a complete proof; `widened` requires the
 *     carried counterexample; `unknown` must never certify admission.
 */
export function verifyCertificate(
  certificate: InclusionCertificate,
  inner: Grammar,
  outer: Grammar,
): VerificationResult {
  if (certificate.format !== 'type-admission/inclusion-cert/v1') {
    return { valid: false, reason: 'unknown certificate format' };
  }
  if (certificate.ruleVersion !== RULE_VERSION) {
    return { valid: false, reason: `rule version ${certificate.ruleVersion} not supported` };
  }
  if (certificate.newGrammarHash !== canonicalHash(inner)) {
    return { valid: false, reason: 'inner grammar hash mismatch' };
  }
  if (certificate.oldGrammarHash !== canonicalHash(outer)) {
    return { valid: false, reason: 'outer grammar hash mismatch' };
  }
  if (certificate.verdict === 'unknown') {
    return { valid: false, reason: 'inconclusive certificate: cannot gate admission' };
  }

  const innerByNt = new Map(inner.productions.map((p) => [p.nt, p]));
  const outerByNt = new Map(outer.productions.map((p) => [p.nt, p]));
  const pairIndex = new Set(certificate.proof.map((pair) => `${pair.newNt}\u0000${pair.oldNt}`));

  if (!pairIndex.has(`${inner.start}\u0000${outer.start}`)) {
    return { valid: false, reason: 'proof lacks the start nonterminal pair' };
  }

  for (const pair of certificate.proof) {
    const innerProd = innerByNt.get(pair.newNt);
    const outerProd = outerByNt.get(pair.oldNt);
    if (innerProd === undefined || outerProd === undefined) {
      return { valid: false, reason: `proof pair ${pair.newNt}/${pair.oldNt} references unknown nonterminal` };
    }
    if (certificate.verdict === 'subset' && pair.matches.length !== innerProd.alts.length) {
      return { valid: false, reason: `pair ${pair.newNt}/${pair.oldNt} does not cover every alternative` };
    }
    for (const witness of pair.matches) {
      const innerAlt = innerProd.alts[witness.newAltIndex];
      const outerAlt = outerProd.alts[witness.oldAltIndex];
      if (innerAlt === undefined || outerAlt === undefined) {
        return { valid: false, reason: 'witness points at a missing alternative' };
      }
      if (!headContained(innerAlt, outerAlt)) {
        return { valid: false, reason: `head not contained for pair ${pair.newNt}/${pair.oldNt}` };
      }
      const innerArgs = innerAlt.args ?? [];
      const outerArgs = outerAlt.args ?? [];
      if (witness.argPairs.length !== requiredPairCount(innerArgs.map((arg) => arg.variance))) {
        return { valid: false, reason: 'argument pair count disagrees with variance' };
      }
      let cursor = 0;
      for (let i = 0; i < innerArgs.length; i++) {
        if (innerArgs[i].variance !== outerArgs[i].variance) {
          return { valid: false, reason: `variance marker differs at position ${i}` };
        }
        const orientation = innerArgs[i].variance === 'contravariant'
          ? [[outerArgs[i].nt, innerArgs[i].nt]]
          : innerArgs[i].variance === 'invariant'
            ? [
                [innerArgs[i].nt, outerArgs[i].nt],
                [outerArgs[i].nt, innerArgs[i].nt],
              ]
            : [[innerArgs[i].nt, outerArgs[i].nt]];
        for (const [expectedInner, expectedOuter] of orientation) {
          const link = witness.argPairs[cursor];
          cursor += 1;
          if (link.newNt !== expectedInner || link.oldNt !== expectedOuter) {
            return { valid: false, reason: 'argument pair has wrong orientation or target' };
          }
          if (!pairIndex.has(`${link.newNt}\u0000${link.oldNt}`)) {
            return { valid: false, reason: 'argument pair is not part of the proof set' };
          }
        }
      }
    }
  }

  if (certificate.verdict === 'widened' && certificate.counterexample === undefined) {
    return { valid: false, reason: 'widened verdict without counterexample' };
  }
  return { valid: certificate.verdict === 'subset' };
}

function requiredPairCount(variances: string[]): number {
  return variances.reduce((sum, variance) => sum + (variance === 'invariant' ? 2 : 1), 0);
}

function headContained(
  inner: Grammar['productions'][number]['alts'][number],
  outer: Grammar['productions'][number]['alts'][number],
): boolean {
  if (inner.typeName !== outer.typeName) return false;
  if ((inner.arrayRank ?? 0) !== (outer.arrayRank ?? 0)) return false;
  if ((inner.args ?? []).length !== (outer.args ?? []).length) return false;
  if ((inner.element ?? '') !== (outer.element ?? '')) return false;
  return assemblyRefSubset(inner.assembly, outer.assembly);
}
