export type ComponentType =
  | 'activity'
  | 'activity-alias'
  | 'service'
  | 'receiver'
  | 'provider';

export interface DataSpec {
  scheme?: string;
  host?: string;
  port?: string;
  path?: string;
  pathPrefix?: string;
  pathPattern?: string;
  mimeType?: string;
}

export interface IntentFilter {
  actions: string[];
  categories: string[];
  data: DataSpec[];
}

export interface ComponentInfo {
  type: ComponentType;
  name: string;
  exported?: boolean;
  explicitExported: boolean;
  permission?: string;
  readPermission?: string;
  writePermission?: string;
  grantUriPermissions?: boolean;
  authorities: string[];
  targetActivity?: string;
  intentFilters: IntentFilter[];
  metaData: Record<string, string>;
}

export interface PermissionDeclaration {
  name: string;
  protectionLevels: string[];
  defaulted: boolean;
}

export interface AndroidManifest {
  packageName: string;
  targetSdk?: number;
  minSdk?: number;
  applicationPermission?: string;
  components: ComponentInfo[];
  permissions: Map<string, PermissionDeclaration>;
  usesPermissions: string[];
}

export type RedirectKind =
  | 'activity-alias'
  | 'startActivity'
  | 'startActivityForResult'
  | 'startService'
  | 'bindService'
  | 'sendBroadcast'
  | 'content-uri-grant'
  | 'unknown';

export interface RedirectEdge {
  source: string;
  target: string;
  kind: RedirectKind;
  requiresPermission?: string;
  intentPassthrough?: boolean;
  note?: string;
}

export type ProtectionLevel =
  | 'normal'
  | 'dangerous'
  | 'signature'
  | 'signatureOrSystem'
  | 'privileged'
  | 'internal'
  | 'unknown';

export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info';

export interface ProtectionInfo {
  permission?: string;
  level: ProtectionLevel;
  customDeclared: boolean;
  defaultedLevel: boolean;
  readPermission?: string;
  writePermission?: string;
}

export interface ComponentSurface {
  name: string;
  type: ComponentType;
  exported: boolean;
  explicitExported: boolean;
  implicitlyExported: boolean;
  reason: string;
  protection: ProtectionInfo;
  reachableViaRedirect: boolean;
  redirectEntries: string[];
}

export interface ChainHop {
  from: string;
  to: string;
  kind: RedirectKind;
  requiresPermission?: string;
  intentPassthrough: boolean;
}

export interface RedirectChain {
  entry: string;
  target: string;
  targetExported: boolean;
  hops: ChainHop[];
  hopCount: number;
  entryAccessible: boolean;
  exploitable: boolean;
  blockedBy?: string;
  intentPassthrough: boolean;
  uriGrantBypass: boolean;
  severity: Severity;
}

export interface RedirectOnlyTarget {
  name: string;
  type: ComponentType;
  shortestHopCount: number;
  exploitable: boolean;
  entries: Array<{
    entry: string;
    hopCount: number;
    exploitable: boolean;
    severity: Severity;
  }>;
}

export interface Finding {
  id: string;
  severity: Severity;
  type: string;
  component?: string;
  message: string;
  evidence: Record<string, unknown>;
}

export interface AnalysisOptions {
  redirects?: RedirectEdge[];
  permissionRegistry?: Record<string, ProtectionLevel>;
  maxHops?: number;
}

export interface AnalysisReport {
  packageName: string;
  targetSdk?: number;
  summary: {
    componentCount: number;
    exportedCount: number;
    implicitlyExportedCount: number;
    unprotectedExportedCount: number;
    redirectOnlyCount: number;
    exploitableChainCount: number;
  };
  components: ComponentSurface[];
  findings: Finding[];
  redirectChains: RedirectChain[];
  redirectOnlyComponents: RedirectOnlyTarget[];
}
