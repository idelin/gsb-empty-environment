import { ctorKey } from "./canonical.ts";
import type {
  BindingTable,
  ConstructorDescriptor,
  DeploymentConfig,
  ParamPermutation,
  RenameRule,
  VarianceMark,
  VersionRange,
  VersionRangeRule,
} from "./types.ts";

export class DeploymentError extends Error {}

export function parseVersionTuple(text: string): number[] {
  if (!/^\d+(\.\d+)*$/.test(text)) {
    throw new DeploymentError(`invalid exact version: ${text}`);
  }
  return text.split(".").map((part) => Number(part));
}

/** Parse ranges such as "*", "1.2.3", ">=1.0.0 <2.0.0". */
export function parseVersionRange(text: string): VersionRange {
  const trimmed = text.trim();
  if (trimmed === "*") {
    return { any: true };
  }
  if (!trimmed.includes(" ")) {
    parseVersionTuple(trimmed);
    return { exact: [trimmed] };
  }
  const range: VersionRange = {};
  for (const clause of trimmed.split(/\s+/)) {
    const match = /^(>=|<=|>|<)(\d+(?:\.\d+)*)$/.exec(clause);
    if (!match) {
      throw new DeploymentError(`invalid range clause: ${clause}`);
    }
    const tuple = parseVersionTuple(match[2]);
    if (match[1] === ">=" || match[1] === ">") {
      range.min = tuple;
    } else {
      range.max = tuple;
    }
  }
  return range;
}

export function rangeContains(range: VersionRange, version: string): boolean {
  const tuple = parseVersionTuple(version);
  if (range.any) {
    return true;
  }
  if (range.exact) {
    return range.exact.includes(version);
  }
  if (range.min && compareTuples(tuple, range.min) < 0) {
    return false;
  }
  if (range.max && compareTuples(tuple, range.max) > 0) {
    return false;
  }
  return true;
}

function compareTuples(a: number[], b: number[]): number {
  const length = Math.max(a.length, b.length);
  for (let i = 0; i < length; i++) {
    const left = a[i] ?? 0;
    const right = b[i] ?? 0;
    if (left !== right) {
      return left - right;
    }
  }
  return 0;
}

/**
 * Validate a deployment configuration against both binding tables.
 * Implicit variance changes are illegal: any difference must be declared
 * through an explicit variance mark.
 */
export function validateDeployment(
  config: DeploymentConfig,
  oldTable: BindingTable,
  newTable: BindingTable,
): void {
  if (config.rulesVersion !== oldTable.rulesVersion) {
    throw new DeploymentError(
      "deployment rulesVersion does not match the certified table release",
    );
  }
  if (!Number.isInteger(config.budget) || config.budget < 0) {
    throw new DeploymentError("budget must be a non-negative integer");
  }
  const oldIndex = indexTable(oldTable);
  const newIndex = indexTable(newTable);

  const renameTargets = new Map<string, string>();
  for (const rule of config.renames) {
    const from = ctorKey(rule.from.assemblyPublicKey, rule.from.typeName);
    const to = ctorKey(rule.to.assemblyPublicKey, rule.to.typeName);
    if (!newIndex.has(from)) {
      throw new DeploymentError(`rename source ${from} absent from new table`);
    }
    if (!oldIndex.has(to)) {
      throw new DeploymentError(`rename target ${to} absent from old table`);
    }
    renameTargets.set(from, to);
  }

  for (const permutation of config.permutations) {
    checkPermutation(permutation, newIndex);
  }

  for (const mark of config.varianceMarks) {
    checkVarianceMark(mark, newIndex);
  }

  for (const rule of config.versionRanges) {
    const key = ctorKey(rule.ctor.assemblyPublicKey, rule.ctor.typeName);
    if (!newIndex.has(key)) {
      throw new DeploymentError(`version range on unknown constructor ${key}`);
    }
  }

  for (const [key, newDescriptors] of newIndex) {
    const mappedKey = renameTargets.get(key) ?? key;
    const oldDescriptors = oldIndex.get(mappedKey);
    if (!oldDescriptors) {
      continue;
    }
    for (const descriptor of newDescriptors) {
      const permutation = config.permutations.find(
        (entry) => ctorKey(entry.ctor.assemblyPublicKey, entry.ctor.typeName) === key,
      );
      const reordered = permutation
        ? descriptor.variances.map((_, position) =>
            descriptor.variances[permutation.mapping[position]],
          )
        : descriptor.variances;
      for (let slot = 0; slot < descriptor.arity; slot++) {
        const mark = config.varianceMarks.find(
          (entry) =>
            ctorKey(entry.ctor.assemblyPublicKey, entry.ctor.typeName) === key &&
            entry.slot === slot,
        );
        const effective = mark ? mark.variance : reordered[slot];
        const oldMatching = oldDescriptors.filter(
          (candidate) => candidate.arity === descriptor.arity,
        );
        for (const old of oldMatching) {
          if (old.variances[slot] !== effective && !mark) {
            throw new DeploymentError(
              `implicit variance change on ${key} slot ${slot} ` +
                `(${old.variances[slot]} -> ${effective}); an explicit varianceMark is required`,
            );
          }
        }
      }
    }
  }
}

function indexTable(
  table: BindingTable,
): Map<string, ConstructorDescriptor[]> {
  const index = new Map<string, ConstructorDescriptor[]>();
  for (const descriptor of table.constructors) {
    const key = ctorKey(descriptor.assemblyPublicKey, descriptor.typeName);
    const list = index.get(key);
    if (list) {
      list.push(descriptor);
    } else {
      index.set(key, [descriptor]);
    }
  }
  return index;
}

function checkPermutation(
  permutation: ParamPermutation,
  index: Map<string, ConstructorDescriptor[]>,
): void {
  const key = ctorKey(permutation.ctor.assemblyPublicKey, permutation.ctor.typeName);
  const descriptors = index.get(key);
  if (!descriptors) {
    throw new DeploymentError(`permutation on unknown constructor ${key}`);
  }
  const arity = descriptors[0].arity;
  const mapping = permutation.mapping;
  if (mapping.length !== arity || new Set(mapping).size !== arity) {
    throw new DeploymentError(`permutation for ${key} must cover [0..${arity})`);
  }
  if (mapping.some((position) => position < 0 || position >= arity)) {
    throw new DeploymentError(`permutation for ${key} contains out-of-range slots`);
  }
}

function checkVarianceMark(
  mark: VarianceMark,
  index: Map<string, ConstructorDescriptor[]>,
): void {
  const key = ctorKey(mark.ctor.assemblyPublicKey, mark.ctor.typeName);
  const descriptors = index.get(key);
  if (!descriptors) {
    throw new DeploymentError(`variance mark on unknown constructor ${key}`);
  }
  if (mark.slot < 0 || mark.slot >= descriptors[0].arity) {
    throw new DeploymentError(`variance mark slot out of range for ${key}`);
  }
}

export interface MappedConstructor {
  descriptor: ConstructorDescriptor;
  range?: VersionRange;
}

/**
 * Every version a range may admit has to be an exact version the old table
 * already permitted for that constructor; otherwise the range itself would be
 * a permit for types the old release never accepted.
 */
export function assertRangesBackedByOldTable(
  mapped: MappedConstructor[],
  oldTable: BindingTable,
): void {
  const oldIndex = indexTable(oldTable);
  for (const entry of mapped) {
    if (!entry.range) {
      continue;
    }
    const key = ctorKey(
      entry.descriptor.assemblyPublicKey,
      entry.descriptor.typeName,
    );
    const oldVersions = new Set(
      (oldIndex.get(key) ?? []).map((descriptor) => descriptor.assemblyVersion),
    );
    if (entry.range.any || entry.range.min || entry.range.max) {
      throw new DeploymentError(
        `open version range on ${key} cannot be proven against finite old permits; ` +
          "restrict it to exact versions present in the old table",
      );
    }
    for (const version of entry.range.exact ?? []) {
      if (!oldVersions.has(version)) {
        throw new DeploymentError(
          `range on ${key} lists ${version}, which the old table never permitted`,
        );
      }
    }
  }
}

/**
 * Project the new table into the old table's coordinate system. Constructors
 * with no old-side counterpart are dropped here and surface as widening
 * witnesses during inclusion (old permits can never gain new types).
 */
export function mapNewTable(
  config: DeploymentConfig,
  oldTable: BindingTable,
  newTable: BindingTable,
): MappedConstructor[] {
  const oldIndex = indexTable(oldTable);
  const result: MappedConstructor[] = [];

  for (const descriptor of newTable.constructors) {
    const key = ctorKey(descriptor.assemblyPublicKey, descriptor.typeName);
    const rename = config.renames.find((entry: RenameRule) =>
      ctorKey(entry.from.assemblyPublicKey, entry.from.typeName) === key,
    );
    const mappedKey = rename
      ? ctorKey(rename.to.assemblyPublicKey, rename.to.typeName)
      : key;
    if (!oldIndex.has(mappedKey)) {
      continue;
    }
    const permutation = config.permutations.find(
      (entry: ParamPermutation) =>
        ctorKey(entry.ctor.assemblyPublicKey, entry.ctor.typeName) === key,
    );
    const marks = config.varianceMarks.filter(
      (entry: VarianceMark) =>
        ctorKey(entry.ctor.assemblyPublicKey, entry.ctor.typeName) === key,
    );
    const rangeRule = config.versionRanges.find(
      (entry: VersionRangeRule) =>
        ctorKey(entry.ctor.assemblyPublicKey, entry.ctor.typeName) === key,
    );

    const mappedName = mappedKey.split("#");
    const variances = descriptor.variances.map((variance, position) => {
      const reordered = permutation
        ? descriptor.variances[permutation.mapping[position]]
        : variance;
      const mark = marks.find((entry) => entry.slot === position);
      return mark ? mark.variance : reordered;
    });
    result.push({
      descriptor: {
        assemblyPublicKey: mappedName[0],
        assemblyVersion: descriptor.assemblyVersion,
        typeName: mappedName[1],
        arity: descriptor.arity,
        variances,
        array: descriptor.array,
        arrayRank: descriptor.arrayRank,
      },
      range: rangeRule?.range,
    });
  }
  return result;
}
