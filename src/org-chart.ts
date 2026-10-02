import { BitemporalRelations, OPEN } from "./bitemporal.ts";
import type { BitemporalRecord } from "./bitemporal.ts";

export { OPEN };
export type { BitemporalRecord };

/**
 * 可追溯员工组织架构时态树。
 *
 * - 汇报线（员工 -> 上级）与部门归属（员工 -> 部门）均为双时态关系：
 *   既带业务生效时间段，也带系统录入时间段。
 * - 支持在任意知悉时刻查询任意业务时刻的汇报链 / 部门归属。
 * - 补录历史调岗、修订历史归属时，重叠的任职时间段自动分裂，旧版本保留可审计。
 */
export class OrgChart {
  private readonly reportingLines = new BitemporalRelations<string>();
  private readonly departmentMemberships = new BitemporalRelations<string>();

  /**
   * 录入汇报关系：employeeId 在业务时间 [validFrom, validTo) 内向 managerId 汇报，
   * 该事实于 sysTime 被系统知悉。
   */
  assignManager(
    employeeId: string,
    managerId: string,
    validFrom: number,
    validTo: number,
    sysTime: number,
  ): void {
    if (employeeId === managerId) {
      throw new RangeError("员工不能向自己汇报");
    }
    this.reportingLines.set(employeeId, managerId, validFrom, validTo, sysTime);
  }

  /**
   * 录入部门归属：employeeId 在业务时间 [validFrom, validTo) 内隶属于 departmentId，
   * 该事实于 sysTime 被系统知悉。
   */
  assignDepartment(
    employeeId: string,
    departmentId: string,
    validFrom: number,
    validTo: number,
    sysTime: number,
  ): void {
    this.departmentMemberships.set(
      employeeId,
      departmentId,
      validFrom,
      validTo,
      sysTime,
    );
  }

  /** 在 knowledgeTime 知悉视角下，employeeId 在 businessTime 业务时刻的直接上级。 */
  managerAt(
    employeeId: string,
    businessTime: number,
    knowledgeTime: number,
  ): string | undefined {
    return this.reportingLines.valueAt(employeeId, businessTime, knowledgeTime);
  }

  /** 在 knowledgeTime 知悉视角下，employeeId 在 businessTime 业务时刻的所属部门。 */
  departmentAt(
    employeeId: string,
    businessTime: number,
    knowledgeTime: number,
  ): string | undefined {
    return this.departmentMemberships.valueAt(
      employeeId,
      businessTime,
      knowledgeTime,
    );
  }

  /**
   * 在 knowledgeTime 知悉视角下，employeeId 在 businessTime 业务时刻的完整汇报链，
   * 从员工本人一直到没有上级的最高管理者。带环检测，防止脏数据导致死循环。
   */
  reportingChain(
    employeeId: string,
    businessTime: number,
    knowledgeTime: number,
  ): string[] {
    const chain: string[] = [employeeId];
    const seen = new Set<string>(chain);
    let current = employeeId;

    for (;;) {
      const manager = this.managerAt(current, businessTime, knowledgeTime);
      if (manager === undefined) return chain;
      if (seen.has(manager)) {
        throw new Error(
          `检测到汇报环：${[...chain, manager].join(" -> ")}`,
        );
      }
      chain.push(manager);
      seen.add(manager);
      current = manager;
    }
  }

  /** 汇报线的全部历史版本（含已被修订封闭的记录），用于审计追溯。 */
  reportingAuditTrail(
    employeeId: string,
  ): readonly BitemporalRecord<string>[] {
    return this.reportingLines.auditTrail(employeeId);
  }

  /** 部门归属的全部历史版本（含已被修订封闭的记录），用于审计追溯。 */
  departmentAuditTrail(
    employeeId: string,
  ): readonly BitemporalRecord<string>[] {
    return this.departmentMemberships.auditTrail(employeeId);
  }
}
