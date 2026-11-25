import { canonicalizeGrammar } from "../src/grammar.ts";
import { issueCertificate } from "../src/certificate.ts";
import { arrayDescriptor } from "../src/canonical.ts";
import type {
  BindingTable,
  ConstructorDescriptor,
  DeploymentConfig,
  GrammarDocument,
  TypeGraph,
  TypeNode,
  Variance,
} from "../src/types.ts";

export const PK_A = "pk-alpha";
export const PK_B = "pk-beta";

export function named(
  id: string,
  typeName: string,
  args: TypeNode[] = [],
  options: Partial<Pick<TypeNode, "assemblyPublicKey" | "assemblyVersion">> = {},
): TypeNode {
  return {
    kind: "named",
    id,
    assemblyPublicKey: options.assemblyPublicKey ?? PK_A,
    assemblyVersion: options.assemblyVersion ?? "1.0.0",
    typeName,
    args,
  };
}

export function ref(target: string): TypeNode {
  return { kind: "ref", target };
}

export function array(id: string, element: TypeNode, rank = 1): TypeNode {
  return { kind: "array", id, rank, element };
}

export function ctor(
  typeName: string,
  variances: Variance[],
  options: Partial<ConstructorDescriptor> = {},
): ConstructorDescriptor {
  return {
    assemblyPublicKey: options.assemblyPublicKey ?? PK_A,
    assemblyVersion: options.assemblyVersion ?? "1.0.0",
    typeName,
    arity: options.arity ?? variances.length,
    variances,
    array: options.array,
    arrayRank: options.arrayRank,
  };
}

export function graph(root: string, ...nodes: TypeNode[]): TypeGraph {
  const map: Record<string, TypeNode> = {};
  for (const node of nodes) {
    if (node.kind !== "ref") {
      map[node.id] = node;
    }
  }
  for (const node of nodes) {
    collectNested(node, map);
  }
  return { root, nodes: map };
}

function collectNested(node: TypeNode, map: Record<string, TypeNode>): void {
  if (node.kind === "ref") {
    return;
  }
  if (node.kind === "array") {
    if (node.element.kind !== "ref") {
      map[node.element.id] = node.element;
      collectNested(node.element, map);
    }
    return;
  }
  for (const arg of node.args) {
    if (arg.kind !== "ref") {
      map[arg.id] = arg;
      collectNested(arg, map);
    }
  }
}

export function oldTable(): BindingTable {
  return {
    tableId: "table-old",
    rulesVersion: "admission-rules/1.0.0",
    constructors: [
      ctor("System.String", []),
      ctor("System.Int32", []),
      ctor("Generic.List", ["out"]),
      ctor("Generic.Box", ["invariant"]),
      ctor("Generic.Func", ["in", "out"]),
      ctor("Generic.Pair", ["out", "out"]),
      ctor("Ambiguous.Type", ["out"], { assemblyPublicKey: PK_B, assemblyVersion: "2.0.0" }),
      arrayDescriptor(1),
      arrayDescriptor(2),
    ],
  };
}

export function tenantGrammar(table: BindingTable): GrammarDocument {
  const find = (typeName: string, pk = PK_A, version = "1.0.0") =>
    table.constructors.find(
      (descriptor) =>
        descriptor.typeName === typeName &&
        descriptor.assemblyPublicKey === pk &&
        descriptor.assemblyVersion === version,
    )!;
  return {
    tenant: "tenant-1",
    start: "S",
    nonterminals: {
      S: [
        { constructor: find("System.String"), children: [] },
        {
          constructor: find("Generic.List"),
          children: ["S"],
        },
        {
          constructor: find("Generic.Box"),
          children: ["S"],
        },
        {
          constructor: find("Generic.Func"),
          children: ["S", "S"],
        },
      ],
      I: [{ constructor: find("System.Int32"), children: [] }],
    },
  };
}

export function baseConfig(overrides: Partial<DeploymentConfig> = {}): DeploymentConfig {
  return {
    rulesVersion: "admission-rules/1.0.0",
    renames: [],
    permutations: [],
    varianceMarks: [],
    versionRanges: [],
    budget: 10_000,
    ...overrides,
  };
}

export function certify(
  newTable: BindingTable,
  newGrammar: GrammarDocument,
  config?: Partial<DeploymentConfig>,
) {
  const old = oldTable();
  const grammar = tenantGrammar(old);
  const issue = issueCertificate({
    oldTable: old,
    newTable,
    oldGrammar: grammar,
    newGrammar,
    config: baseConfig(config),
  });
  return { issue, old, oldGrammar: grammar };
}

export function canonicalOld() {
  const table = oldTable();
  return { table, grammar: canonicalizeGrammar(tenantGrammar(table), table) };
}
