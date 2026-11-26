export type RuntimeWorld =
  | "page"
  | "content-script"
  | "service-worker"
  | "extension-page"
  | "offscreen-document"
  | "sandboxed-extension-page";

export type Severity = "info" | "low" | "medium" | "high" | "critical";

export interface ContentScriptDeclaration {
  matches: string | string[];
  exclude_matches?: string | string[];
  include_globs?: string | string[];
  exclude_globs?: string | string[];
  js?: string | string[];
  css?: string | string[];
  run_at?: string;
  all_frames?: boolean;
  match_about_blank?: boolean;
  match_origin_as_fallback?: boolean;
  world?: "ISOLATED" | "MAIN";
}

export interface WebAccessibleResourceEntry {
  resources: string | string[];
  matches?: string | string[];
  extension_ids?: string | string[];
  use_dynamic_url?: boolean;
}

export interface ExternallyConnectable {
  matches?: string | string[];
  ids?: string | string[];
  accept_tls_channel_id?: boolean;
}

export interface DnrRuleResource {
  id: string;
  enabled: boolean;
  path: string;
}

export interface DeclarativeNetRequestManifest {
  rule_resources: DnrRuleResource[];
}

export interface ManifestV3 {
  manifest_version: 3;
  name?: string;
  version?: string;
  description?: string;
  permissions?: string | string[];
  optional_permissions?: string | string[];
  host_permissions?: string | string[];
  optional_host_permissions?: string | string[];
  content_scripts?: ContentScriptDeclaration[];
  web_accessible_resources?: WebAccessibleResourceEntry[];
  externally_connectable?: ExternallyConnectable;
  declarative_net_request?: DeclarativeNetRequestManifest;
  background?: {
    service_worker?: string;
    type?: "module" | "classic";
  };
  action?: { default_popup?: string };
  browser_action?: { default_popup?: string };
  page_action?: { default_popup?: string };
  options_ui?: { page?: string; open_in_tab?: boolean };
  options_page?: string;
  devtools_page?: string;
  side_panel?: { default_path?: string };
  chrome_url_overrides?: Record<string, string>;
  content_security_policy?: {
    extension_pages?: string;
    sandbox?: string;
  };
  [key: string]: unknown;
}

export type OriginCheckPolicy =
  | "none"
  | "presence-only"
  | "same-origin"
  | { mode: "allowlist"; patterns: string[] };

export type SenderBindingPolicy = "none" | "payload-origin" | "verified";

export type TokenBindingField =
  | "origin"
  | "tabId"
  | "action"
  | "permissions"
  | "target"
  | "route";

export interface SignedTokenPolicy {
  mode: "signed";
  authority?: string;
  boundTo?: TokenBindingField[];
}

export type CapabilityTokenPolicy =
  | { mode: "none" }
  | { mode: "static"; secretRef?: string }
  | SignedTokenPolicy;

export type TargetScopePolicy =
  | "none"
  | "sender-origin"
  | "tab-permissions"
  | { mode: "allowlist"; patterns: string[] };

export type PrivilegedOperationKind =
  | "cookie-read"
  | "script-injection"
  | "dnr-bypass"
  | "fetch-proxy"
  | "storage-read"
  | "custom";

export interface PrivilegedOperation {
  kind: PrivilegedOperationKind;
  target?: string;
  targetKind?: "url" | "domain" | "pattern";
  requiredPermissions?: string[];
  description?: string;
}

export interface MessageRoute {
  id: string;
  from: RuntimeWorld;
  to: RuntimeWorld;
  messageType: string | string[];
  fromHandler?: string;
  toHandler?: string;
  actionId?: string;
  expectedPageOrigins?: string[];
  originCheck?: OriginCheckPolicy;
  senderBinding?: SenderBindingPolicy;
  tabPermissionBound?: boolean;
  capabilityToken?: CapabilityTokenPolicy;
  targetScope?: TargetScopePolicy;
  operation?: PrivilegedOperation;
  description?: string;
}

export type SecurityIssueCode =
  | "MISSING_ORIGIN_CHECK"
  | "ORIGIN_CHECK_PRESENCE_ONLY"
  | "ORIGIN_CHECK_UNTRUSTED"
  | "ORIGIN_ALLOWLIST_TOO_BROAD"
  | "SENDER_ORIGIN_UNBOUND"
  | "PAYLOAD_ORIGIN_SPOOFABLE"
  | "TAB_PERMISSION_NOT_BOUND"
  | "CAPABILITY_TOKEN_MISSING"
  | "CAPABILITY_TOKEN_STATIC"
  | "CAPABILITY_TOKEN_BINDING_INCOMPLETE"
  | "TARGET_SCOPE_MISSING"
  | "OPERATION_NOT_GRANTED"
  | "TARGET_OUTSIDE_TAB_ORIGIN"
  | "WAR_PRIVILEGED_PAGE_EXPOSED"
  | "WAR_BROAD_EXPOSURE"
  | "EXTERNALLY_CONNECTABLE_WILDCARD"
  | "EXTERNALLY_CONNECTABLE_UNSCOPED"
  | "DNR_RULE_PRIORITY_OVERRIDE"
  | "DNR_ALLOW_ALL_REQUESTS_OVERRIDE"
  | "CONFUSED_DEPUTY_PATH";

export interface SecurityIssue {
  code: SecurityIssueCode;
  severity: Severity;
  message: string;
  routeId?: string;
  resource?: string;
  evidence?: Record<string, unknown>;
}

export interface CapabilityTokenClaims {
  v: 1;
  routeId: string;
  actionId: string;
  origin: string;
  tabId: number;
  permissions: string[];
  target?: string;
  issuedAt: number;
  expiresAt: number;
  nonce: string;
}

export interface AttackEntryPoint {
  id?: string;
  pageOrigin: string;
  messageType?: string | string[];
  tabPermissions?: string[];
  via?: "content-script" | "externally-connectable" | "web-accessible-resource" | "unknown";
  resource?: string;
}

export interface AttackPathHop {
  routeId: string;
  from: RuntimeWorld;
  to: RuntimeWorld;
  messageType: string;
  missingControls: string[];
}

export interface AttackPath {
  entryOrigin: string;
  entryId?: string;
  sink: PrivilegedOperation;
  sinkRouteId: string;
  hopCount: number;
  hops: AttackPathHop[];
  missingControls: string[];
  severity: Severity;
}

export interface AttackSearchResult {
  maxHops: number;
  paths: AttackPath[];
  bestPath?: AttackPath;
  exploredStates: number;
}

export interface SecurityAnalysisInput {
  manifest: ManifestV3;
  routes: MessageRoute[];
  entries?: AttackEntryPoint[];
  dnrRules?: unknown[];
  untrustedOrigins?: string[];
  maxHops?: number;
  maxPaths?: number;
}

export interface SecurityAnalysisResult {
  issues: SecurityIssue[];
  attackPaths: AttackPath[];
  safe: boolean;
}

export interface RuntimeSender {
  origin?: string | null;
  tabId?: number;
  tabUrl?: string;
  tabPermissions?: string[];
  documentId?: string;
}

export interface RuntimeAuthorizationRequest {
  routeId: string;
  messageType: string;
  sender: RuntimeSender;
  token?: string;
  payload?: unknown;
}

export interface AuthorizationDecision {
  allowed: boolean;
  code:
    | "ALLOWED"
    | "UNKNOWN_ROUTE"
    | "MESSAGE_TYPE_MISMATCH"
    | "MISSING_SENDER_ORIGIN"
    | "OPAQUE_SENDER_ORIGIN"
    | "ORIGIN_CHECK_MISSING"
    | "ORIGIN_CHECK_PRESENCE_ONLY"
    | "ORIGIN_SCOPE_VIOLATION"
    | "SENDER_BINDING_MISSING"
    | "PAYLOAD_ORIGIN_SPOOFED"
    | "MISSING_TAB_CONTEXT"
    | "TAB_PERMISSION_NOT_BOUND"
    | "SENDER_OUTSIDE_TAB_PERMISSIONS"
    | "CAPABILITY_TOKEN_REQUIRED"
    | "CAPABILITY_TOKEN_STATIC"
    | "CAPABILITY_TOKEN_INVALID"
    | "CAPABILITY_TOKEN_BINDING_INCOMPLETE"
    | "TARGET_SCOPE_MISSING"
    | "OPERATION_NOT_GRANTED"
    | "TARGET_OUTSIDE_ORIGIN_BOUNDARY"
    | "TARGET_NOT_IN_TAB_PERMISSIONS"
    | "TARGET_OUTSIDE_ALLOWLIST";
  reason: string;
  tokenClaims?: CapabilityTokenClaims;
}

export interface IssueTokenInput {
  routeId: string;
  origin: string;
  tabId: number;
  tabPermissions: string[];
  actionId?: string;
  target?: string;
  ttlMs?: number;
  nonce?: string;
}
