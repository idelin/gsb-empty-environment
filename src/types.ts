/**
 * 双时态文件历史：业务生效区间（valid time）+ 录入版本（knowledge version）。
 *
 * - 业务生效区间为半开区间 [validFrom, validTo)，validTo 可为 Infinity 表示开放区间。
 * - 录入版本 knowledgeVersion 单调递增，表示该修订被知悉（录入）的版本。
 * - 同一时点被多条修订覆盖时，按 knowledgeVersion 大者优先，相同再按 revisionId 大者优先。
 * - 撤销（revoke）记录在 revokedAt，仅对 knowledgeTime >= revokedAt 的查询生效，
 *   即撤销只影响之后的知悉版本，历史知悉版本仍可见。
 */

export interface Revision<A = Record<string, unknown>> {
  /** 文件标识 */
  readonly fileId: string;
  /** 修订 ID，全局单调递增，作为同录入版本下的决胜因子 */
  readonly revisionId: number;
  /** 业务生效起点（含） */
  readonly validFrom: number;
  /** 业务生效终点（不含），Infinity 表示开放 */
  readonly validTo: number;
  /** 录入（知悉）版本 */
  readonly knowledgeVersion: number;
  /** 属性快照 */
  readonly attributes: A;
  /** 撤销发生的录入版本；未撤销为 undefined。仅对 knowledgeTime >= revokedAt 的查询生效 */
  revokedAt?: number;
}

export interface QueryResult<A = Record<string, unknown>> {
  readonly fileId: string;
  readonly attributes: A;
  /** 命中值的具体来源修订 */
  readonly revision: Revision<A>;
}

export interface RecordInput<A = Record<string, unknown>> {
  readonly fileId: string;
  readonly validFrom: number;
  readonly validTo?: number;
  readonly attributes: A;
  /** 可选显式录入版本；缺省自动递增。必须不小于上一次分配的版本 */
  readonly knowledgeVersion?: number;
}
