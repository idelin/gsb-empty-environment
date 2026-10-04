export interface RoleDefinition {
  name: string;
  permissions: readonly string[];
  inherits?: readonly string[];
}

export interface GrantRequest {
  roles: readonly RoleDefinition[];
  required: readonly string[];
  sensitive?: readonly string[];
  /** Maximum number of roles allowed in a single grant. Defaults to 25. */
  maxRoles?: number;
}

/** Hard limit on the role catalog size; keeps the exact search tractable. */
export const MAX_ROLE_CATALOG_SIZE = 25;

export interface BlockedGrantor {
  role: string;
  sensitivePermissions: string[];
}

export type PermissionWitness =
  | { kind: 'missing'; permission: string }
  | { kind: 'sensitive-conflict'; permission: string }
  | { kind: 'forced-sensitive'; permission: string; blockedBy: BlockedGrantor[] };

export interface GrantSuccess {
  ok: true;
  roles: string[];
  roleCount: number;
  grantedPermissions: string[];
  excessPermissions: string[];
  warnings: string[];
}

export interface GrantUncoverable {
  ok: false;
  reason: 'uncoverable';
  witnesses: PermissionWitness[];
  warnings: string[];
}

export interface GrantRoleLimitExceeded {
  ok: false;
  reason: 'role-limit-exceeded';
  maxRoles: number;
  minimumRolesNeeded: number;
  optimalCombination: string[];
  warnings: string[];
}

export interface GrantInvalid {
  ok: false;
  reason: 'invalid';
  errors: string[];
}

export type GrantResult =
  | GrantSuccess
  | GrantUncoverable
  | GrantRoleLimitExceeded
  | GrantInvalid;
