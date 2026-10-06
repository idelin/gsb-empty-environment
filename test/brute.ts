/**
 * Independent brute-force paginator used to verify src/paginate.ts.
 * Enumerates every possible set of page breakpoints over the flattened
 * document and picks the lexicographically best cost tuple
 * [violations, pageCount, remainingSquareSum]. Only practical for short
 * documents, which is exactly what the verifier uses.
 */

export interface BruteCost {
  violations: number;
  pageCount: number;
  remainingSquareSum: number;
}

interface Unit {
  big: boolean;
  pseudo: number;
  height: number;
}

export function bruteForce(
  paragraphs: ReadonlyArray<ReadonlyArray<number>>,
  pageHeight: number,
): BruteCost {
  // Flatten into units; oversized lines become singleton "big" units and cut
  // paragraphs into pseudo-paragraphs (same spec as the library).
  const units: Unit[] = [];
  let pseudo = 0;
  let lastPara = -1;
  let fresh = true;
  paragraphs.forEach((lines, para) => {
    for (const height of lines) {
      if (height > pageHeight) {
        units.push({ big: true, pseudo: -1, height });
        fresh = true;
      } else {
        if (fresh || para !== lastPara) {
          pseudo++;
          fresh = false;
        }
        units.push({ big: false, pseudo, height });
        lastPara = para;
      }
    }
  });

  const n = units.length;
  const startByPseudo = new Map<number, number>();
  const endByPseudo = new Map<number, number>();
  for (let i = 0; i < n; i++) {
    if (units[i].big) continue;
    if (!startByPseudo.has(units[i].pseudo)) startByPseudo.set(units[i].pseudo, i);
    endByPseudo.set(units[i].pseudo, i + 1);
  }

  // Does a normal page covering units [i, j) break the two-line rule?
  const violates = (i: number, j: number): boolean => {
    const first = units[i];
    if (startByPseudo.get(first.pseudo)! < i) {
      const frag = Math.min(j, endByPseudo.get(first.pseudo)!) - i;
      if (frag < 2) return true;
    }
    const last = units[j - 1];
    if (endByPseudo.get(last.pseudo)! > j) {
      const frag = j - Math.max(i, startByPseudo.get(last.pseudo)!);
      if (frag < 2) return true;
    }
    return false;
  };

  let best: [number, number, number] = [Infinity, Infinity, Infinity];
  const consider = (v: number, p: number, s: number): void => {
    if (v < best[0] || (v === best[0] && (p < best[1] || (p === best[1] && s < best[2])))) {
      best = [v, p, s];
    }
  };

  const rec = (i: number, v: number, p: number, s: number): void => {
    if (v > best[0] || (v === best[0] && p > best[1])) return; // prune
    if (i === n) {
      consider(v, p, s);
      return;
    }
    if (units[i].big) {
      const rem = pageHeight - units[i].height;
      rec(i + 1, v, p + 1, s + rem * rem);
      return;
    }
    let h = 0;
    for (let j = i; j < n && !units[j].big; j++) {
      h += units[j].height;
      if (h > pageHeight) break;
      const rem = pageHeight - h;
      rec(j + 1, v + (violates(i, j + 1) ? 1 : 0), p + 1, s + rem * rem);
    }
  };
  rec(0, 0, 0, 0);

  return { violations: best[0], pageCount: best[1], remainingSquareSum: best[2] };
}
