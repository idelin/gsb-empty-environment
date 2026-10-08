import type {
  Diagnostic,
  FootnoteDefinition,
  Heading,
  LineInfo,
  ParsedDocument,
  Range,
  Reference,
} from './types.ts';

const HEADING_RE = /^(#{1,6})[ \t]+(.*?)[ \t]*$/;
const DEFINITION_RE = /^\[\^([^\]]+)\]:[ \t]?(.*)$/;
const REFERENCE_RE = /\[\^([^\]]+)\]/g;

function makeRange(line: number, startCol: number, endCol: number): Range {
  return {
    start: { line, character: startCol },
    end: { line, character: endCol },
  };
}

function collectReferences(
  text: string,
  offset: number,
  inDefinitionBody: boolean,
  out: LineInfo['references'],
): void {
  for (const match of text.matchAll(REFERENCE_RE)) {
    out.push({
      name: match[1],
      startCol: offset + match.index,
      endCol: offset + match.index + match[0].length,
      inDefinitionBody,
    });
  }
}

/** Parse a single line into content-only symbols (columns are line-relative). */
export function parseLine(text: string): LineInfo {
  const info: LineInfo = { references: [] };

  const heading = HEADING_RE.exec(text);
  if (heading) {
    info.heading = {
      level: heading[1].length,
      text: heading[2],
      startCol: 0,
      endCol: text.length,
    };
  }

  const definition = DEFINITION_RE.exec(text);
  if (definition) {
    const name = definition[1];
    const body = definition[2];
    const bodyStartCol = text.length - body.length;
    info.definition = {
      name,
      body,
      nameStartCol: 2,
      nameEndCol: 2 + name.length,
      bodyStartCol,
      endCol: text.length,
    };
    collectReferences(body, bodyStartCol, true, info.references);
  } else {
    collectReferences(text, 0, false, info.references);
  }

  return info;
}

/** Build the cross-line symbol tables and diagnostics from per-line parses. */
export function assemble(infos: LineInfo[]): ParsedDocument {
  const headings: Heading[] = [];
  const references: Reference[] = [];
  const definitions: FootnoteDefinition[] = [];
  const definitionMap = new Map<string, FootnoteDefinition>();
  const duplicates: FootnoteDefinition[] = [];

  infos.forEach((info, line) => {
    if (info.heading) {
      headings.push({
        level: info.heading.level,
        text: info.heading.text,
        line,
        range: makeRange(line, info.heading.startCol, info.heading.endCol),
      });
    }

    let currentDefinition: FootnoteDefinition | null = null;
    if (info.definition) {
      const d = info.definition;
      currentDefinition = {
        name: d.name,
        body: d.body,
        line,
        range: makeRange(line, d.nameStartCol, d.nameEndCol),
        references: [],
      };
      definitions.push(currentDefinition);
      if (definitionMap.has(d.name)) {
        duplicates.push(currentDefinition);
      } else {
        definitionMap.set(d.name, currentDefinition);
      }
    }

    for (const r of info.references) {
      const reference: Reference = {
        key: '',
        name: r.name,
        index: references.length,
        line,
        range: makeRange(line, r.startCol, r.endCol),
        inDefinitionBody: r.inDefinitionBody,
      };
      references.push(reference);
      if (currentDefinition) currentDefinition.references.push(reference);
    }
  });

  const ordinals = new Map<string, number>();
  for (const reference of references) {
    const ordinal = ordinals.get(reference.name) ?? 0;
    reference.key = ordinal === 0 ? reference.name : `${reference.name}#${ordinal}`;
    ordinals.set(reference.name, ordinal + 1);
  }

  const diagnostics: Diagnostic[] = [];
  const diagnosticsByReference = new Map<string, Diagnostic[]>();

  for (const duplicate of duplicates) {
    const first = definitionMap.get(duplicate.name)!;
    diagnostics.push({
      code: 'duplicate-definition',
      severity: 'error',
      range: duplicate.range,
      message: `Duplicate footnote definition "[^${duplicate.name}]"; the first definition (line ${first.line + 1}) is kept.`,
    });
  }

  for (const reference of references) {
    if (!definitionMap.has(reference.name)) {
      const diagnostic: Diagnostic = {
        code: 'undefined-reference',
        severity: 'error',
        range: reference.range,
        message: `Reference "[^${reference.name}]" has no footnote definition.`,
      };
      diagnostics.push(diagnostic);
      const list = diagnosticsByReference.get(reference.key) ?? [];
      list.push(diagnostic);
      diagnosticsByReference.set(reference.key, list);
    }
  }

  for (const cycle of findCircularDefinitions(definitionMap)) {
    const members = [...cycle].sort();
    for (const name of cycle) {
      const definition = definitionMap.get(name)!;
      diagnostics.push({
        code: 'circular-footnote',
        severity: 'warning',
        range: definition.range,
        message: `Circular footnote definition involving: ${members.map((n) => `[^${n}]`).join(', ')}.`,
        relatedNames: members,
      });
    }
  }

  diagnostics.sort(
    (a, b) =>
      a.range.start.line - b.range.start.line ||
      a.range.start.character - b.range.start.character ||
      a.code.localeCompare(b.code),
  );

  return {
    headings,
    references,
    definitions,
    definitionMap,
    duplicates,
    diagnostics,
    diagnosticsByReference,
  };
}

/** Tarjan SCC over kept definitions; returns SCCs that form reference cycles. */
function findCircularDefinitions(
  definitionMap: Map<string, FootnoteDefinition>,
): string[][] {
  const graph = new Map<string, string[]>();
  for (const [name, definition] of definitionMap) {
    const edges: string[] = [];
    for (const reference of definition.references) {
      if (definitionMap.has(reference.name)) edges.push(reference.name);
    }
    graph.set(name, edges);
  }

  const indexOf = new Map<string, number>();
  const lowLink = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const cycles: string[][] = [];
  let counter = 0;

  function strongConnect(node: string): void {
    indexOf.set(node, counter);
    lowLink.set(node, counter);
    counter++;
    stack.push(node);
    onStack.add(node);

    for (const next of graph.get(node) ?? []) {
      if (!indexOf.has(next)) {
        strongConnect(next);
        lowLink.set(node, Math.min(lowLink.get(node)!, lowLink.get(next)!));
      } else if (onStack.has(next)) {
        lowLink.set(node, Math.min(lowLink.get(node)!, indexOf.get(next)!));
      }
    }

    if (lowLink.get(node) === indexOf.get(node)) {
      const scc: string[] = [];
      let member: string;
      do {
        member = stack.pop()!;
        onStack.delete(member);
        scc.push(member);
      } while (member !== node);
      const isCycle =
        scc.length > 1 || (graph.get(scc[0]) ?? []).includes(scc[0]);
      if (isCycle) cycles.push(scc);
    }
  }

  for (const node of graph.keys()) {
    if (!indexOf.has(node)) strongConnect(node);
  }
  return cycles;
}

/** Full one-shot parse of a document. */
export function parseDocument(text: string): ParsedDocument {
  return assemble(text.split('\n').map(parseLine));
}
