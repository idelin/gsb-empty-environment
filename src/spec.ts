// 法定集合（quorum）规格：单调布尔公式。
// 原子为 "atLeast k of G"（从节点组 G 中至少选 k 个），
// 可用 all（且）/ any（或）组合。单调性保证剪枝与贪心搜索的正确性。

export type SpecJson =
  | { atLeast: number; of: string[] | "*" }
  | { all: SpecJson[] }
  | { any: SpecJson[] };

export type Spec =
  | { kind: "atLeast"; k: number; nodes: number[] }
  | { kind: "all"; children: Spec[] }
  | { kind: "any"; children: Spec[] };

export function compileSpec(
  json: SpecJson,
  indexOf: Map<string, number>,
  nodeCount: number,
): Spec {
  if (typeof json !== "object" || json === null) {
    throw new Error("规格必须是对象");
  }
  if ("atLeast" in json) {
    const k = json.atLeast;
    if (!Number.isInteger(k) || k < 0) {
      throw new Error(`atLeast 必须是非负整数，得到 ${JSON.stringify(k)}`);
    }
    let nodes: number[];
    if (json.of === "*") {
      nodes = Array.from({ length: nodeCount }, (_, i) => i);
    } else if (Array.isArray(json.of)) {
      const seen = new Set<number>();
      for (const name of json.of) {
        const idx = indexOf.get(name);
        if (idx === undefined) throw new Error(`未知节点: ${JSON.stringify(name)}`);
        seen.add(idx);
      }
      nodes = [...seen].sort((a, b) => a - b);
    } else {
      throw new Error('of 必须是节点名数组或 "*"');
    }
    return { kind: "atLeast", k, nodes };
  }
  if ("all" in json) {
    if (!Array.isArray(json.all)) throw new Error("all 必须是数组");
    return { kind: "all", children: json.all.map((c) => compileSpec(c, indexOf, nodeCount)) };
  }
  if ("any" in json) {
    if (!Array.isArray(json.any)) throw new Error("any 必须是数组");
    return { kind: "any", children: json.any.map((c) => compileSpec(c, indexOf, nodeCount)) };
  }
  throw new Error("规格必须包含 atLeast / all / any 之一");
}

// 三值求值：1 = 必真，-1 = 必假，0 = 未定。
// assign[i]: 0 未定，1 在集合内，2 在集合外。
export function triEval(spec: Spec, assign: Uint8Array): number {
  switch (spec.kind) {
    case "atLeast": {
      let inCount = 0;
      let undecided = 0;
      for (const v of spec.nodes) {
        if (assign[v] === 1) inCount++;
        else if (assign[v] === 0) undecided++;
      }
      if (inCount >= spec.k) return 1;
      if (inCount + undecided < spec.k) return -1;
      return 0;
    }
    case "all": {
      let unknown = false;
      for (const child of spec.children) {
        const r = triEval(child, assign);
        if (r === -1) return -1;
        if (r === 0) unknown = true;
      }
      return unknown ? 0 : 1;
    }
    case "any": {
      let unknown = false;
      for (const child of spec.children) {
        const r = triEval(child, assign);
        if (r === 1) return 1;
        if (r === 0) unknown = true;
      }
      return unknown ? 0 : -1;
    }
  }
}

// 精确求值：未定节点视为不在集合内。
export function evalExact(spec: Spec, assign: Uint8Array): boolean {
  switch (spec.kind) {
    case "atLeast": {
      let inCount = 0;
      for (const v of spec.nodes) if (assign[v] === 1) inCount++;
      return inCount >= spec.k;
    }
    case "all":
      return spec.children.every((c) => evalExact(c, assign));
    case "any":
      return spec.children.some((c) => evalExact(c, assign));
  }
}

export function specToString(spec: Spec, names: string[]): string {
  switch (spec.kind) {
    case "atLeast": {
      const group =
        spec.nodes.length === names.length
          ? "*"
          : spec.nodes.map((i) => names[i]).join(",");
      return `至少${spec.k}个{${group}}`;
    }
    case "all":
      return `(${spec.children.map((c) => specToString(c, names)).join(" 且 ")})`;
    case "any":
      return `(${spec.children.map((c) => specToString(c, names)).join(" 或 ")})`;
  }
}
