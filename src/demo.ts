import { compareRuleSets } from "./compare.ts";
import { domain, newPolicy, oldPolicy } from "./policies.ts";
import { renderReport } from "./report.ts";

const result = compareRuleSets(oldPolicy, newPolicy, domain);
console.log(renderReport(result, domain, oldPolicy.name, newPolicy.name));
