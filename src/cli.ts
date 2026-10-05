import * as fs from 'fs';
import { fmtSearchResult } from './format';
import { findDifference } from './search';
import { Query, RelationSchema, Value } from './types';

interface RelationSpec {
  name: string;
  attrs: string[];
  domain?: Value[];
  domains?: Record<string, Value[]>;
}

interface Spec {
  domain: Value[];
  relations: RelationSpec[];
  maxRows: number;
  left: Query;
  right: Query;
}

function toSchemas(spec: Spec): RelationSchema[] {
  if (!Array.isArray(spec.domain) || spec.domain.length === 0) {
    throw new Error('spec.domain must be a non-empty array of values');
  }
  return spec.relations.map((r) => ({
    name: r.name,
    attrs: r.attrs,
    domains: r.attrs.map((a) => {
      const d = r.domains?.[a] ?? r.domain ?? spec.domain;
      if (!Array.isArray(d) || d.length === 0) {
        throw new Error(`no domain for attribute "${a}" of relation "${r.name}"`);
      }
      return d;
    }),
  }));
}

function main(argv: string[]): number {
  const specPath = argv[2];
  if (!specPath) {
    console.error('usage: qdiff <spec.json>');
    console.error('spec: { domain, relations: [{name, attrs, domain?, domains?}], maxRows, left, right }');
    return 2;
  }
  const spec = JSON.parse(fs.readFileSync(specPath, 'utf8')) as Spec;
  const schemas = toSchemas(spec);
  const result = findDifference(schemas, spec.left, spec.right, spec.maxRows);
  console.log(fmtSearchResult(result));
  return result.equivalentWithinBound ? 0 : 1;
}

process.exit(main(process.argv));
