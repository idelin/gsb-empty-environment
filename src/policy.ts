import { Rational, ZERO } from './rational.ts';
import type {
  ClassId,
  EquivalenceClassInput,
  GenerationInput,
  PrivacyPolicyInput,
  ResolvedPrivacyPolicy,
  SecretId,
  SignedWorkModule,
  NormalizedWorkModule,
  PrivacyIssue,
} from './types.ts';
import { EMPTY_CANDIDATE } from './types.ts';

export class PrivacyConfigurationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PrivacyConfigurationError';
  }
}

export function generationToBigInt(generation: GenerationInput): bigint {
  if (typeof generation === 'bigint') return generation;
  if (typeof generation === 'number') {
    if (!Number.isSafeInteger(generation) || generation < 0) {
      throw new PrivacyConfigurationError(`Invalid policy generation: ${generation}`);
    }
    return BigInt(generation);
  }
  if (typeof generation === 'string' && /^\d+$/.test(generation)) return BigInt(generation);
  throw new PrivacyConfigurationError(`Invalid policy generation: ${String(generation)}`);
}

function normalizeClasses(
  input: PrivacyPolicyInput,
): {
  classes: Map<ClassId, readonly SecretId[]>;
  classOfSecret: Record<SecretId, ClassId>;
} {
  const classes = new Map<ClassId, readonly SecretId[]>();
  const classOfSecret: Record<SecretId, ClassId> = {};

  if (input.equivalenceClasses === undefined) {
    for (const secret of input.secrets) {
      classes.set(secret, [secret]);
      classOfSecret[secret] = secret;
    }
    return { classes, classOfSecret };
  }

  const list: readonly EquivalenceClassInput[] = Array.isArray(input.equivalenceClasses)
    ? input.equivalenceClasses
    : Object.entries(input.equivalenceClasses).map(([id, secrets]) => ({ id, secrets }));

  for (const klass of list) {
    if (!klass.id || classes.has(klass.id)) {
      throw new PrivacyConfigurationError(`Duplicate or empty equivalence class id: ${klass.id}`);
    }
    if (klass.secrets.length === 0) {
      throw new PrivacyConfigurationError(`Equivalence class ${klass.id} is empty`);
    }
    const members: SecretId[] = [];
    for (const secret of klass.secrets) {
      if (!input.secrets.includes(secret)) {
        throw new PrivacyConfigurationError(`Class ${klass.id} contains unknown secret ${secret}`);
      }
      if (classOfSecret[secret] !== undefined) {
        throw new PrivacyConfigurationError(`Secret ${secret} appears in multiple classes`);
      }
      classOfSecret[secret] = klass.id;
      members.push(secret);
    }
    classes.set(klass.id, Object.freeze(members));
  }

  for (const secret of input.secrets) {
    if (classOfSecret[secret] === undefined) {
      throw new PrivacyConfigurationError(`Secret ${secret} is not assigned to an equivalence class`);
    }
  }
  return { classes, classOfSecret };
}

function buildProtectedPairs(
  input: PrivacyPolicyInput,
  classes: Map<ClassId, readonly SecretId[]>,
): readonly (readonly [SecretId, SecretId, ClassId, ClassId])[] {
  const classPairs: [ClassId, ClassId][] = [];

  if (input.protectedClassPairs === undefined || input.protectedClassPairs === 'all') {
    const ids = [...classes.keys()];
    for (let i = 0; i < ids.length; i += 1) {
      for (let j = i + 1; j < ids.length; j += 1) classPairs.push([ids[i], ids[j]]);
    }
  } else {
    for (const pair of input.protectedClassPairs) {
      if (!classes.has(pair[0]) || !classes.has(pair[1])) {
        throw new PrivacyConfigurationError(`Protected class pair contains unknown class: ${pair.join('/')}`);
      }
      classPairs.push([pair[0], pair[1]]);
    }
  }

  const secretPairs: [SecretId, SecretId, ClassId, ClassId][] = [];
  for (const [classA, classB] of classPairs) {
    const membersA = classes.get(classA) ?? [];
    const membersB = classes.get(classB) ?? [];
    for (const a of membersA) {
      for (const b of membersB) {
        secretPairs.push([a, b, classA, classB]);
        secretPairs.push([b, a, classB, classA]);
      }
    }
  }
  return secretPairs;
}

export function resolvePolicy(input: PrivacyPolicyInput): ResolvedPrivacyPolicy {
  if (input.secrets.length === 0) throw new PrivacyConfigurationError('At least one secret is required');
  const secretSet = new Set<SecretId>();
  for (const secret of input.secrets) {
    if (secret.length === 0) throw new PrivacyConfigurationError('Secret ids must not be empty');
    if (secretSet.has(secret)) throw new PrivacyConfigurationError(`Duplicate secret id: ${secret}`);
    secretSet.add(secret);
  }

  const ratioCap = Rational.from(input.ratioCap);
  if (!ratioCap.isPositive()) throw new PrivacyConfigurationError('ratioCap R must be positive');
  const posteriorMultiplier = Rational.from(input.posteriorMultiplier ?? 3n);
  if (!posteriorMultiplier.isPositive()) {
    throw new PrivacyConfigurationError('posteriorMultiplier must be positive');
  }

  const rawPriors = input.secrets.map((secret) => {
    const prior = input.priors[secret];
    if (prior === undefined) throw new PrivacyConfigurationError(`Missing prior for secret ${secret}`);
    const rational = Rational.from(prior);
    if (!rational.isPositive()) throw new PrivacyConfigurationError(`Prior for ${secret} must be positive`);
    return rational;
  });

  const rawTotal = rawPriors.reduce<Rational>((sum, prior) => sum.add(prior), ZERO);
  const normalize = input.normalizePriors ?? true;
  const priors = {} as Record<SecretId, Rational>;
  input.secrets.forEach((secret, index) => {
    priors[secret] = normalize ? rawPriors[index].div(rawTotal) : rawPriors[index];
  });
  if (!normalize && !rawTotal.eq(1n)) {
    throw new PrivacyConfigurationError(`Priors must sum to 1 when normalization is disabled; got ${rawTotal}`);
  }

  const generation = generationToBigInt(input.policyGeneration);
  const { classes, classOfSecret } = normalizeClasses(input);
  const protectedSecretPairs = buildProtectedPairs(input, classes);

  return Object.freeze({
    secrets: Object.freeze([...input.secrets]),
    priors: Object.freeze(priors),
    ratioCap,
    policyGeneration: generation,
    classes,
    classOfSecret: Object.freeze(classOfSecret),
    protectedSecretPairs: Object.freeze(protectedSecretPairs),
    posteriorMultiplier,
  });
}

function moduleIssue(code: string, message: string): PrivacyIssue {
  return { code, message };
}

export function normalizeWorkModule(
  module: SignedWorkModule,
  policy: ResolvedPrivacyPolicy,
): { module: NormalizedWorkModule | null; issues: readonly PrivacyIssue[] } {
  const issues: PrivacyIssue[] = [];
  let generation: bigint;
  try {
    generation = generationToBigInt(module.policyGeneration);
  } catch (error) {
    issues.push(moduleIssue('INVALID_GENERATION', (error as Error).message));
    generation = -1n;
  }

  if (generation !== policy.policyGeneration) {
    issues.push(
      moduleIssue(
        'POLICY_GENERATION_MISMATCH',
        `Module generation ${generation.toString()} does not match policy ${policy.policyGeneration.toString()}`,
      ),
    );
  }

  const resourceOf: Record<string, string> = {};
  if (module.resourceOf) {
    for (const [candidate, resource] of Object.entries(module.resourceOf)) {
      resourceOf[String(candidate)] = resource;
    }
  }

  const tables = {} as Record<SecretId, Record<string, Rational>>;
  const outcomeSet = new Set<string>();

  for (const secret of policy.secrets) {
    const sourceTable = module.tables[secret];
    if (!sourceTable) {
      issues.push(moduleIssue('MISSING_SECRET_TABLE', `Module ${module.id} has no table for secret ${secret}`));
      continue;
    }
    const table: Record<string, Rational> = {};
    let total = ZERO;
    for (const [rawCandidate, rawProbability] of Object.entries(sourceTable)) {
      const candidate = String(rawCandidate);
      let probability: Rational;
      try {
        probability = Rational.from(rawProbability);
      } catch (error) {
        issues.push(moduleIssue('INVALID_PROBABILITY', (error as Error).message));
        continue;
      }
      if (probability.lt(0n)) {
        issues.push(moduleIssue('NEGATIVE_PROBABILITY', `Negative probability for ${module.id}/${secret}/${candidate}`));
        continue;
      }
      table[candidate] = probability;
      total = total.add(probability);
      if (!probability.isZero()) {
        if (candidate !== EMPTY_CANDIDATE && module.resourceOf && resourceOf[candidate] === undefined) {
          issues.push(moduleIssue('UNRESOLVED_CANDIDATE', `Candidate ${candidate} does not resolve to a resource`));
        }
        outcomeSet.add(candidate === EMPTY_CANDIDATE ? candidate : resourceOf[candidate] ?? candidate);
      }
    }
    if (!total.eq(1n)) {
      issues.push(moduleIssue('TABLE_NOT_NORMALIZED', `Table for ${module.id}/${secret} sums to ${total}, expected 1`));
    }
    tables[secret] = table;
  }

  if (issues.some((issue) => ['INVALID_PROBABILITY', 'NEGATIVE_PROBABILITY', 'TABLE_NOT_NORMALIZED', 'MISSING_SECRET_TABLE', 'UNRESOLVED_CANDIDATE'].includes(issue.code))) {
    return { module: null, issues };
  }

  const normalized: NormalizedWorkModule = {
    id: module.id,
    policyGeneration: generation,
    signature: module.signature,
    ...(module.signerKeyId ? { signerKeyId: module.signerKeyId } : {}),
    ...(module.codeHash ? { codeHash: module.codeHash } : {}),
    resourceOf: Object.freeze(resourceOf),
    tables: Object.freeze(tables),
    publicOutcomes: Object.freeze([...outcomeSet].sort()),
  };
  return { module: normalized, issues };
}
