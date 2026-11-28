/**
 * Type definitions mirroring the Kubernetes admission.k8s.io/v1 API surface
 * consumed by the ValidatingAdmissionPolicy evaluation engine.
 */

export type Operation = 'CREATE' | 'UPDATE' | 'DELETE' | 'CONNECT';

export type FailurePolicy = 'Fail' | 'Ignore';

export type ValidationAction = 'Deny' | 'Warn' | 'Audit';

export type ParameterNotFoundAction = 'Allow' | 'Deny';

export type SelectorOperator = 'In' | 'NotIn' | 'Exists' | 'DoesNotExist';

/** Plain JSON value used throughout the engine. */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export interface LabelSelectorRequirement {
  key: string;
  operator: SelectorOperator;
  values?: string[];
}

export interface LabelSelector {
  matchLabels?: Record<string, string>;
  matchExpressions?: LabelSelectorRequirement[];
}

export interface NamedRuleWithOperations {
  apiGroups?: string[];
  apiVersions?: string[];
  resources?: string[];
  operations?: Array<Operation | '*'>;
  resourceNames?: string[];
  scope?: 'Cluster' | 'Namespaced' | '*';
}

export interface MatchResources {
  namespaceSelector?: LabelSelector;
  objectSelector?: LabelSelector;
  resourceRules?: NamedRuleWithOperations[];
  excludeResourceRules?: NamedRuleWithOperations[];
  matchPolicy?: 'Exact' | 'Equivalent';
}

export interface ParamRef {
  name?: string;
  namespace?: string;
  selector?: LabelSelector;
  parameterNotFoundAction?: ParameterNotFoundAction;
}

export interface ParamKind {
  apiVersion?: string;
  kind?: string;
}

export interface MatchCondition {
  name: string;
  expression: string;
}

export interface Variable {
  name: string;
  expression: string;
}

export interface Validation {
  expression: string;
  message?: string;
  messageExpression?: string;
  reason?: string;
}

export interface ValidatingAdmissionPolicySpec {
  paramKind?: ParamKind;
  matchConstraints?: MatchResources;
  matchConditions?: MatchCondition[];
  variables?: Variable[];
  validations?: Validation[];
  failurePolicy?: FailurePolicy;
}

export interface ValidatingAdmissionPolicy {
  apiVersion?: string;
  kind?: string;
  metadata?: { name?: string; labels?: Record<string, string> };
  spec: ValidatingAdmissionPolicySpec;
}

export interface ValidatingAdmissionPolicyBindingSpec {
  policyName: string;
  matchResources?: MatchResources;
  paramRef?: ParamRef;
  validationActions?: ValidationAction[];
}

export interface ValidatingAdmissionPolicyBinding {
  apiVersion?: string;
  kind?: string;
  metadata?: { name?: string; labels?: Record<string, string> };
  spec: ValidatingAdmissionPolicyBindingSpec;
}

/** Group/version/resource tuple of the request target. */
export interface GroupVersionResource {
  apiGroup: string;
  apiVersion: string;
  resource: string;
  subresource?: string;
}

export interface AdmissionRequest {
  uid?: string;
  operation: Operation;
  resource: GroupVersionResource;
  /** Object being admitted (may be undefined for DELETE). */
  object?: JsonValue;
  oldObject?: JsonValue;
  name?: string;
  namespace?: string;
  /** Labels of the owning namespace (for namespaceSelector evaluation). */
  namespaceObject?: { metadata?: { labels?: Record<string, string> } } | null;
  /** Whether the resource is cluster scoped. */
  clusterScoped?: boolean;
  userInfo?: { username?: string; groups?: string[] };
  options?: JsonValue;
}

/** A parameter object as stored by the engine's parameter store. */
export interface ParameterObject {
  apiVersion?: string;
  kind?: string;
  metadata?: {
    name?: string;
    namespace?: string;
    labels?: Record<string, string>;
  };
  [key: string]: JsonValue | undefined;
}

export interface ValidationFailure {
  binding: string;
  policy: string;
  validationIndex: number;
  message: string;
  actions: ValidationAction[];
  parameter?: string;
}

export interface BindingEvaluation {
  binding: string;
  policy: string;
  /** True when matchConstraints/matchConditions selected this binding. */
  matched: boolean;
  skippedReason?: string;
  /** Resolved parameter references (namespace/name), empty for parameterless policies. */
  parameterKeys: string[];
  failures: ValidationFailure[];
  validationActions: ValidationAction[];
  /** True when the binding itself failed (parameterNotFound=Deny or Fail-policy error). */
  errored: boolean;
  errorMessage?: string;
}

export interface AdmissionDecision {
  allowed: boolean;
  denied: boolean;
  warnings: string[];
  auditAnnotations: Record<string, string>;
  failures: ValidationFailure[];
  bindingEvaluations: BindingEvaluation[];
  reasons: string[];
}
