import { RetentionPolicyError } from "./errors.ts";

export type DataCategory = "metrics" | "logs" | "events" | "traces" | "security";

export interface RetentionPolicy {
  category: DataCategory;
  retentionMs: number;
}

export class RetentionPolicyTable {
  private readonly policies = new Map<DataCategory, number>();

  constructor(policies: RetentionPolicy[]) {
    for (const policy of policies) {
      if (policy.retentionMs < 0) {
        throw new RetentionPolicyError(
          `Retention for category "${policy.category}" must be non-negative`,
        );
      }
      this.policies.set(policy.category, policy.retentionMs);
    }
  }

  has(category: DataCategory): boolean {
    return this.policies.has(category);
  }

  get(category: DataCategory): number {
    const retentionMs = this.policies.get(category);
    if (retentionMs === undefined) {
      throw new RetentionPolicyError(
        `No retention policy configured for category "${category}"`,
      );
    }
    return retentionMs;
  }

  expiryFor(category: DataCategory, createdAtMs: number): number {
    return createdAtMs + this.get(category);
  }
}
