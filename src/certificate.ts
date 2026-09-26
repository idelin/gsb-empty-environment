import type { Degradation, Layout, Page, PageConfig } from './paginate.ts';

export interface PageChecks {
  nonEmpty: boolean;
  withinCapacity: boolean;
  widowsOrphans: boolean;
  keepWithNext: boolean;
  footnoteAttribution: boolean;
}

export interface PageCertificate {
  page: number;
  capacity: number;
  bodyUsed: number;
  footnoteSeparator: number;
  footnotes: { id: string; height: number }[];
  slack: number;
  overflow: boolean;
  relaxedConstraints: string[];
  checks: PageChecks;
}

export interface Certificate {
  documentId: string;
  pageCount: number;
  totalCost: number;
  pages: PageCertificate[];
  footnoteAttribution: Record<string, number>;
  degradations: Degradation[];
  fixedPoint?: { converged: boolean; iterations: number };
  optimality?: {
    verified: boolean;
    layoutsEnumerated: number;
    dpCost: number;
    exhaustiveMinCost: number;
  };
  stats: { candidatesTried: number; candidatesPruned: number };
}

/** Independently re-checks every hard constraint against the produced pages. */
export function checkLayout(pages: Page[], config: PageConfig): PageChecks[] {
  // First-reference page of each footnote, derived from the atoms themselves.
  const firstRefPage = new Map<string, number>();
  for (const page of pages) {
    for (const atom of page.atoms) {
      if (atom.kind === 'line') {
        for (const id of atom.footnoteRefs) {
          if (!firstRefPage.has(id)) firstRefPage.set(id, page.index);
        }
      }
    }
  }
  return pages.map((page, p) => {
    const last = page.atoms[page.atoms.length - 1];
    const next = pages[p + 1]?.atoms[0];

    let widowsOrphans = true;
    if (
      last?.kind === 'line' &&
      next?.kind === 'line' &&
      last.blockId === next.blockId
    ) {
      const before = last.lineIndex + 1;
      const after = last.lineCount - before;
      widowsOrphans = before >= config.minOrphanLines && after >= config.minWidowLines;
    }

    const footnoteAttribution = page.footnotes.every(
      (fn) => firstRefPage.get(fn.id) === page.index,
    );

    return {
      nonEmpty: page.atoms.length > 0,
      withinCapacity: page.overflow
        ? page.used > config.pageCapacity
        : page.used <= config.pageCapacity,
      widowsOrphans,
      keepWithNext: last?.kind !== 'heading',
      footnoteAttribution,
    };
  });
}

export function buildCertificate(
  documentId: string,
  layout: Layout,
  config: PageConfig,
  extras: {
    fixedPoint?: { converged: boolean; iterations: number };
    optimality?: Certificate['optimality'];
  } = {},
): Certificate {
  const checks = checkLayout(layout.pages, config);
  const footnoteAttribution: Record<string, number> = {};
  for (const page of layout.pages) {
    for (const fn of page.footnotes) footnoteAttribution[fn.id] = page.index;
  }
  return {
    documentId,
    pageCount: layout.pages.length,
    totalCost: layout.cost,
    pages: layout.pages.map((page, i) => ({
      page: page.index,
      capacity: config.pageCapacity,
      bodyUsed: page.bodyHeight,
      footnoteSeparator: page.footnoteSeparator,
      footnotes: page.footnotes,
      slack: page.slack,
      overflow: page.overflow,
      relaxedConstraints: page.relaxedConstraints,
      checks: checks[i],
    })),
    footnoteAttribution,
    degradations: layout.degradations,
    fixedPoint: extras.fixedPoint,
    optimality: extras.optimality,
    stats: layout.stats,
  };
}

const ok = (b: boolean) => (b ? 'ok' : 'VIOLATED');

export function formatCertificate(cert: Certificate): string {
  const lines: string[] = [];
  lines.push(`certificate for "${cert.documentId}": ${cert.pageCount} page(s), total cost ${cert.totalCost}`);
  if (cert.fixedPoint) {
    lines.push(
      `  fixed point: ${cert.fixedPoint.converged ? `converged in ${cert.fixedPoint.iterations} iteration(s)` : 'NOT FOUND'}`,
    );
  }
  for (const p of cert.pages) {
    const fn = p.footnotes.map((f) => `${f.id}(h=${f.height})`).join(', ') || '-';
    lines.push(
      `  page ${p.page}: capacity=${p.capacity} body=${p.bodyUsed} footnotes=[${fn}] separator=${p.footnoteSeparator} slack=${p.slack}` +
        `${p.overflow ? ' OVERFLOW' : ''}` +
        `${p.relaxedConstraints.length ? ` relaxed=[${p.relaxedConstraints.join(',')}]` : ''}`,
    );
    lines.push(
      `    checks: non-empty=${ok(p.checks.nonEmpty)} capacity=${ok(p.checks.withinCapacity)} widows/orphans=${ok(p.checks.widowsOrphans)} keep-with-next=${ok(p.checks.keepWithNext)} footnote-attribution=${ok(p.checks.footnoteAttribution)}`,
    );
  }
  const attribution = Object.entries(cert.footnoteAttribution)
    .map(([id, page]) => `${id}->page ${page}`)
    .join(', ');
  lines.push(`  footnote attribution: ${attribution || '-'}`);
  if (cert.optimality) {
    lines.push(
      `  optimality: ${cert.optimality.verified ? 'VERIFIED' : 'NOT VERIFIED'} ` +
        `(dp cost ${cert.optimality.dpCost}, exhaustive min ${cert.optimality.exhaustiveMinCost}, ${cert.optimality.layoutsEnumerated} layouts enumerated)`,
    );
  }
  lines.push(
    `  search: ${cert.stats.candidatesTried} candidates tried, ${cert.stats.candidatesPruned} pruned/rolled back`,
  );
  if (cert.degradations.length === 0) {
    lines.push('  degradations: none');
  } else {
    for (const d of cert.degradations) lines.push(`  degradation [${d.kind}]: ${d.message}`);
  }
  return lines.join('\n');
}
