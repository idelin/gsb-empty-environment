export type SqlValue =
  | { readonly kind: "null" }
  | { readonly kind: "enum"; readonly value: string }
  | { readonly kind: "id"; readonly name: string };

export type Expr =
  | { readonly kind: "column"; readonly table?: string; readonly column: string }
  | { readonly kind: "const"; readonly value: SqlValue }
  | { readonly kind: "eq"; readonly left: Expr; readonly right: Expr }
  | { readonly kind: "isNull"; readonly expr: Expr }
  | { readonly kind: "and"; readonly args: readonly Expr[] }
  | { readonly kind: "or"; readonly args: readonly Expr[] }
  | { readonly kind: "not"; readonly expr: Expr };

export interface Projection {
  readonly name: string;
  readonly expr: Expr;
}

export type JoinType = "inner" | "left";

export type Plan =
  | {
      readonly kind: "scan";
      readonly table: string;
      readonly alias?: string;
    }
  | {
      readonly kind: "join";
      readonly joinType: JoinType;
      readonly left: Plan;
      readonly right: Plan;
      readonly on: Expr;
    }
  | {
      readonly kind: "filter";
      readonly input: Plan;
      readonly predicate: Expr;
    }
  | {
      readonly kind: "project";
      readonly input: Plan;
      readonly columns: readonly Projection[];
    }
  | {
      readonly kind: "unionAll";
      readonly inputs: readonly Plan[];
    };

export type RowValues = Readonly<Record<string, unknown>>;
export type Database = Readonly<Record<string, readonly RowValues[]>>;

export interface ColumnSchema {
  readonly kind?: "enum" | "id" | "any";
  readonly enumValues?: readonly string[];
  readonly nullable?: boolean;
}

export interface TableSchema {
  readonly columns?: Readonly<Record<string, ColumnSchema>>;
  readonly maxRows?: number;
}

export interface DatabaseSchema {
  readonly tables: Readonly<Record<string, TableSchema>>;
}

export interface TenantPolicyRule {
  readonly subject?: string;
  readonly tenant?: string | null;
  readonly table?: string;
  readonly allow: boolean;
}

export interface TenantReadPolicy {
  readonly version?: string;
  readonly policyVersion?: string;
  readonly revision?: string;
  readonly subject?: string;
  readonly principal?: string;
  readonly allowedTenants?: readonly string[] | ReadonlySet<string>;
  readonly visibleTenants?: readonly string[] | ReadonlySet<string>;
  readonly tenants?: readonly string[] | ReadonlySet<string>;
  readonly tenant?: string;
  readonly grants?: Readonly<Record<string, readonly string[] | ReadonlySet<string>>>;
  readonly tenantsBySubject?: Readonly<Record<string, readonly string[] | ReadonlySet<string>>>;
  readonly rules?: readonly TenantPolicyRule[];
  readonly tenantColumn?: string;
  readonly tenantColumns?: Readonly<Record<string, string>>;
  readonly allowNullTenant?: boolean;
  readonly allowAll?: boolean;
  readonly denyAll?: boolean;
  readonly canRead?: (
    subject: string,
    tenant: string | null,
    context: { readonly table: string; readonly row: Readonly<Record<string, SqlValue>> },
  ) => boolean;
  readonly rowVisible?: (
    row: Readonly<Record<string, SqlValue>>,
    context: { readonly subject: string; readonly table: string },
  ) => boolean;
}

export interface ExecutionCredential {
  readonly format: "plan-execution-credential-v1";
  readonly planDigest: string;
  readonly referencePlanDigest: string;
  readonly policyVersion: string;
  readonly certificateDigest: string;
  readonly token: string;
}

export interface RepresentativeDatabase {
  readonly [table: string]: readonly Readonly<Record<string, SqlValue>>[];
}

export interface RepresentativeColumnDomain {
  readonly name: string;
  readonly kind: "enum" | "id" | "any";
  readonly nullable: boolean;
  readonly enumMode: "closed" | "open" | "none";
  readonly enumValues: readonly string[];
  readonly freshEnumClasses: "canonical" | "single" | "none";
  readonly idMode: "open" | "none";
  readonly freshIdClasses: "canonical" | "single" | "none";
}

export interface RepresentativeTableDomain {
  readonly name: string;
  readonly maxRows: number;
  readonly columns: readonly RepresentativeColumnDomain[];
}

export interface RepresentativeDomain {
  readonly tables: readonly RepresentativeTableDomain[];
  readonly identifierConstants: readonly string[];
  readonly enumLiterals: readonly string[];
  readonly rowCounts: readonly number[];
  readonly freshIdentifierClasses: "canonical-first-use";
  readonly freshEnumClasses: "canonical-first-use";
}

export interface RepresentativeClass {
  readonly key: string;
  readonly database: RepresentativeDatabase;
  readonly tags: readonly string[];
}

export interface PartitionCertificate {
  readonly format: "bag-partition-certificate-v1";
  readonly referencePlanDigest: string;
  readonly candidatePlanDigest: string;
  readonly policyVersion: string;
  readonly domain: RepresentativeDomain;
  readonly equivalent: boolean;
  readonly classes: readonly RepresentativeClass[];
  readonly classCount: number;
  readonly digest: string;
}

export interface BagCounterexample {
  readonly classKey: string;
  readonly database: RepresentativeDatabase;
  readonly referenceRows: readonly (readonly SqlValue[])[];
  readonly candidateRows: readonly (readonly SqlValue[])[];
  readonly missing: readonly { readonly row: readonly SqlValue[]; readonly count: number }[];
  readonly extra: readonly { readonly row: readonly SqlValue[]; readonly count: number }[];
}

export interface AdmissionInput {
  readonly reference?: Plan;
  readonly referencePlan?: Plan;
  readonly candidate?: Plan;
  readonly candidatePlan?: Plan;
  readonly policy: TenantReadPolicy;
  readonly schema?: DatabaseSchema;
  readonly candidatePolicy?: TenantReadPolicy | null;
  readonly maxRepresentativeClasses?: number;
}

export interface AdmissionResult {
  readonly admitted: boolean;
  readonly ok: boolean;
  readonly allowed?: boolean;
  readonly authorized?: boolean;
  readonly reason: string;
  readonly planDigest: string;
  readonly referencePlanDigest: string;
  readonly policyVersion: string;
  readonly credential?: ExecutionCredential;
  readonly certificate?: PartitionCertificate;
  readonly counterexample?: BagCounterexample;
}

export interface CertificateCheckResult {
  readonly ok: boolean;
  readonly valid: boolean;
  readonly expectedClasses: number;
  readonly checkedClasses: number;
  readonly missing: readonly string[];
  readonly unexpected: readonly string[];
  readonly domainMatches: boolean;
  readonly errors: readonly string[];
}
