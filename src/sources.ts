// Covariance graph from shared underlying error sources.
//
// Model: each input x_i is a linear combination of independent error
// sources s_j with gains g_ij, so Sigma = G D G^T with D the diagonal of
// source variances. Sources declared once are counted once: duplicate
// contributions of the same source to the same input are merged by adding
// gains (g1*s + g2*s = (g1+g2)*s), never double counted. Sources with
// identical variance AND identical gain vectors are flagged as suspected
// duplicates of the same physical error source.

import { Rational, type RationalInput } from './rational.ts';
import { checkPSD, zeros, type Matrix } from './matrix.ts';

export interface SourceDecl {
  id: string;
  // Exactly one of std / variance is required.
  std?: RationalInput;
  variance?: RationalInput;
}

export interface Contribution {
  source: string;
  gain?: RationalInput; // defaults to 1
}

export interface InputDecl {
  id: string;
  value: RationalInput;
  contributions?: Contribution[];
}

export interface GraphEdge {
  between: [string, string];
  sharedSources: string[];
}

export interface SourceModel {
  inputIds: string[];
  values: Rational[];
  covariance: Matrix;
  sources: { id: string; variance: Rational }[];
  edges: GraphEdge[];
  warnings: string[];
}

export function buildSourceModel(inputs: InputDecl[], sourceDecls: SourceDecl[]): SourceModel {
  const warnings: string[] = [];

  const inputIds = inputs.map((x) => x.id);
  if (new Set(inputIds).size !== inputIds.length) {
    throw new Error(`Duplicate input id in: ${inputIds.join(', ')}`);
  }

  const variances = new Map<string, Rational>();
  for (const s of sourceDecls) {
    if (variances.has(s.id)) {
      throw new Error(`Duplicate error source declaration: '${s.id}'`);
    }
    if ((s.std === undefined) === (s.variance === undefined)) {
      throw new Error(`Source '${s.id}': provide exactly one of std / variance`);
    }
    const variance =
      s.variance !== undefined ? Rational.of(s.variance) : Rational.of(s.std as RationalInput).square();
    if (variance.sign() < 0) {
      throw new Error(`Source '${s.id}': negative variance ${variance.toString()}`);
    }
    variances.set(s.id, variance);
  }

  // Gain matrix G (inputs x sources), merging duplicate contributions.
  const sourceIds = sourceDecls.map((s) => s.id);
  const sourceIndex = new Map(sourceIds.map((id, j) => [id, j]));
  const n = inputs.length;
  const k = sourceIds.length;
  const G: Matrix = zeros(n, k);
  inputs.forEach((inp, i) => {
    const seen = new Set<string>();
    for (const c of inp.contributions ?? []) {
      const j = sourceIndex.get(c.source);
      if (j === undefined) {
        throw new Error(`Input '${inp.id}' references unknown source '${c.source}'`);
      }
      const gain = Rational.of(c.gain ?? 1);
      if (seen.has(c.source)) {
        warnings.push(
          `Input '${inp.id}': duplicate contribution of source '${c.source}' merged ` +
            `(gains add, counted once)`,
        );
        G[i][j] = G[i][j].add(gain);
      } else {
        seen.add(c.source);
        G[i][j] = gain;
      }
    }
  });

  // Sigma = G D G^T, exact. Zero-variance sources contribute nothing.
  const covariance: Matrix = zeros(n, n);
  for (let j = 0; j < k; j++) {
    const v = variances.get(sourceIds[j]) as Rational;
    if (v.isZero()) continue;
    for (let a = 0; a < n; a++) {
      if (G[a][j].isZero()) continue;
      for (let b = a; b < n; b++) {
        if (G[b][j].isZero()) continue;
        const term = G[a][j].mul(v).mul(G[b][j]);
        covariance[a][b] = covariance[a][b].add(term);
        if (b !== a) covariance[b][a] = covariance[b][a].add(term);
      }
    }
  }

  // Covariance graph: edge between inputs sharing at least one live source.
  const edges: GraphEdge[] = [];
  for (let a = 0; a < n; a++) {
    for (let b = a + 1; b < n; b++) {
      const shared = sourceIds.filter(
        (id, j) =>
          !G[a][j].isZero() && !G[b][j].isZero() && !(variances.get(id) as Rational).isZero(),
      );
      if (shared.length > 0) edges.push({ between: [inputIds[a], inputIds[b]], sharedSources: shared });
    }
  }

  // Suspected duplicate sources: identical variance and identical gain
  // vector across all inputs. If they are the same physical error, the
  // covariance is understated by treating them as independent.
  for (let a = 0; a < k; a++) {
    for (let b = a + 1; b < k; b++) {
      const va = variances.get(sourceIds[a]) as Rational;
      const vb = variances.get(sourceIds[b]) as Rational;
      if (va.cmp(vb) !== 0) continue;
      let same = true;
      for (let i = 0; i < n; i++) {
        if (G[i][a].cmp(G[i][b]) !== 0) {
          same = false;
          break;
        }
      }
      if (same) {
        warnings.push(
          `Sources '${sourceIds[a]}' and '${sourceIds[b]}' have identical variance and ` +
            `gain signature; if they are the same underlying error, merge them to avoid ` +
            `understating covariance`,
        );
      }
    }
  }

  // Sigma = G D G^T is PSD by construction; verify anyway (exact check).
  const psd = checkPSD(covariance);
  if (!psd.psd) {
    throw new Error('Internal error: source-model covariance is not PSD');
  }

  return {
    inputIds,
    values: inputs.map((x) => Rational.of(x.value)),
    covariance,
    sources: sourceIds.map((id) => ({ id, variance: variances.get(id) as Rational })),
    edges,
    warnings,
  };
}
