/** 演示：导出计划检查、违规来源链、过滤缩减人数、共享计算节点 */
import { checkExportPlan, formatReport } from "./checker.ts";
import { Sensitivity } from "./model.ts";
import type { ExportPlan } from "./model.ts";

const plan: ExportPlan = {
  name: "用户数据月度导出",
  nodes: [
    { kind: "source", id: "name", field: "name", level: Sensitivity.Confidential, population: 10000 },
    { kind: "source", id: "phone", field: "phone", level: Sensitivity.Secret, population: 10000 },
    { kind: "source", id: "income", field: "income", level: Sensitivity.Confidential, population: 10000 },

    // 共享计算节点：两个派生字段都依赖它
    { kind: "derive", id: "name_phone_pair", field: "name_phone_pair", inputs: ["name", "phone"], expression: "concat(name, phone)" },
    { kind: "derive", id: "contact_key", field: "contact_key", inputs: ["name_phone_pair"], expression: "sha256(name_phone_pair)" },
    { kind: "derive", id: "mailing_token", field: "mailing_token", inputs: ["name_phone_pair"], expression: "hmac(name_phone_pair, salt)" },

    // 过滤：仅保留高价值客户，人数 10000 -> 150
    { kind: "filter", id: "hv_customers", field: "hv_customers", inputs: ["income"], predicate: "lifetime_value > 100000", remainingPopulation: 150 },

    // 全量聚合：各城市分组人数充足，允许降级到 Internal
    { kind: "aggregate", id: "avg_income_by_city", field: "avg_income_by_city", inputs: ["income"], groupBy: ["city"], groupSizes: [4200, 3100, 2700], minGroupSize: 100, downgradeTo: Sensitivity.Internal },

    // 过滤后聚合：最小组仅 8 人 < 100，降级被拒绝，保持 Confidential
    { kind: "aggregate", id: "avg_income_by_city_hv", field: "avg_income_by_city_hv", inputs: ["hv_customers"], groupBy: ["city"], groupSizes: [80, 62, 8], minGroupSize: 100, downgradeTo: Sensitivity.Internal },
  ],
  exports: [
    // 只导出派生字段（原字段 name/phone 被"隐藏"），目标环境仅允许 Internal
    { nodeId: "contact_key", allowedLevel: Sensitivity.Internal },
    { nodeId: "mailing_token", allowedLevel: Sensitivity.Internal },
    { nodeId: "avg_income_by_city", allowedLevel: Sensitivity.Internal },
    { nodeId: "avg_income_by_city_hv", allowedLevel: Sensitivity.Internal },
  ],
};

console.log(formatReport(checkExportPlan(plan)));
