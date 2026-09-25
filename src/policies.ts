import { and, inSet, intCmp } from "./conditions.ts";
import type { DomainSpec, RuleSet } from "./types.ts";

/**
 * Declared finite analysis universe. Every attribute is explicit; the
 * evaluator reads nothing else. Integer axes are bounded and the report
 * prints these bounds so the finite-scope limitation is never implicit.
 */
export const domain: DomainSpec = {
  role: { kind: "set", values: ["guest", "viewer", "editor", "admin"] },
  env: { kind: "set", values: ["prod", "staging", "dev"] },
  resource: {
    kind: "set",
    values: ["dashboard", "report", "logs", "admin_panel"],
  },
  mfa: { kind: "set", values: [true, false] },
  age: { kind: "intRange", min: 0, max: 2 },
  risk: { kind: "intRange", min: 0, max: 1 },
};

/**
 * OLD rule set. Deny-overrides, default deny.
 */
export const oldPolicy: RuleSet = {
  name: "old-policy",
  defaultEffect: "deny",
  rules: [
    {
      id: "r_admin_panel_allow",
      effect: "allow",
      when: and(inSet("role", ["admin"]), inSet("resource", ["admin_panel"])),
    },
    {
      id: "r_admin_logs_allow",
      effect: "allow",
      when: and(inSet("role", ["admin"]), inSet("resource", ["logs"])),
    },
    {
      id: "r_editor_report_allow",
      effect: "allow",
      when: and(
        inSet("role", ["editor", "admin"]),
        inSet("resource", ["report"]),
        inSet("env", ["prod", "staging"]),
      ),
    },
    {
      id: "r_viewer_dashboard_allow",
      effect: "allow",
      when: and(
        inSet("role", ["viewer", "editor", "admin"]),
        inSet("resource", ["dashboard"]),
        inSet("mfa", [true]),
      ),
    },
    {
      id: "r_dev_logs_allow",
      effect: "allow",
      when: and(
        inSet("env", ["dev"]),
        inSet("resource", ["logs"]),
        inSet("role", ["viewer", "editor", "admin"]),
      ),
    },
    {
      id: "r_guest_deny",
      effect: "deny",
      when: inSet("role", ["guest"]),
    },
    {
      id: "r_age_deny",
      effect: "deny",
      when: intCmp("age", ">=", 2),
    },
  ],
};

/**
 * NEW rule set — same language, intentionally different text:
 * - admin allow rules renamed (explanation only);
 * - report allow tightened to editor only (allow -> deny for admin);
 * - dashboard allow broadened (mfa dropped) but a new prod-no-mfa deny
 *   keeps prod mfa=false denied (explanation only there);
 * - dev logs rule split into two disjoint rules by role (explanation only);
 * - age deny threshold raised 2 -> 3 (deny -> allow where an allow exists);
 * - new explicit deny for editor staging reports (allow -> deny).
 */
export const newPolicy: RuleSet = {
  name: "new-policy",
  defaultEffect: "deny",
  rules: [
    {
      id: "r_age_deny",
      effect: "deny",
      when: intCmp("age", ">=", 3),
    },
    {
      id: "r_prod_no_mfa_deny",
      effect: "deny",
      when: and(inSet("env", ["prod"]), inSet("mfa", [false])),
    },
    {
      id: "r_guest_deny",
      effect: "deny",
      when: inSet("role", ["guest"]),
    },
    {
      id: "r_staging_editor_report_deny",
      effect: "deny",
      when: and(
        inSet("role", ["editor"]),
        inSet("env", ["staging"]),
        inSet("resource", ["report"]),
      ),
    },
    {
      id: "r_dev_logs_admin",
      effect: "allow",
      when: and(
        inSet("env", ["dev"]),
        inSet("resource", ["logs"]),
        inSet("role", ["admin"]),
      ),
    },
    {
      id: "r_dev_logs_general",
      effect: "allow",
      when: and(
        inSet("env", ["dev"]),
        inSet("resource", ["logs"]),
        inSet("role", ["viewer", "editor"]),
      ),
    },
    {
      id: "r_viewer_dashboard_broadened",
      effect: "allow",
      when: and(
        inSet("role", ["viewer", "editor", "admin"]),
        inSet("resource", ["dashboard"]),
      ),
    },
    {
      id: "r_editor_report_tightened",
      effect: "allow",
      when: and(
        inSet("role", ["editor"]),
        inSet("resource", ["report"]),
        inSet("env", ["prod", "staging"]),
      ),
    },
    {
      id: "r_admin_logs_allow_v2",
      effect: "allow",
      when: and(inSet("role", ["admin"]), inSet("resource", ["logs"])),
    },
    {
      id: "r_admin_panel_allow_v2",
      effect: "allow",
      when: and(inSet("role", ["admin"]), inSet("resource", ["admin_panel"])),
    },
  ],
};
