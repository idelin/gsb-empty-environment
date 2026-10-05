import type { NodeSpec, TargetGrouping } from "./types.ts";

export const scenarioNodes: NodeSpec[] = [
  { id: "root", name: "root", parent: null, pinned: true, acl: ["disk:read"] },
  { id: "eng", name: "eng", parent: "root", pinned: true, acl: ["eng:read", "eng:write"] },
  { id: "ops", name: "ops", parent: "root", pinned: true, acl: ["ops:read", "ops:admin"] },
  { id: "shared", name: "shared", parent: "root", pinned: true, acl: ["shared:read"] },
  { id: "backend", name: "backend", parent: "eng", acl: ["backend:deploy"] },
  {
    id: "api",
    name: "api",
    parent: "backend",
    refs: ["../db", "../../../shared/docs"],
  },
  { id: "db", name: "db", parent: "backend", pinned: true, acl: ["db:schema"] },
  {
    id: "frontend",
    name: "frontend",
    parent: "eng",
    refs: ["../backend/api", "../../ops/scripts"],
  },
  {
    id: "ml",
    name: "ml",
    parent: "ops",
    acl: ["ml:gpu"],
    refs: ["../scripts", "../../shared/docs"],
  },
  { id: "datasets", name: "datasets", parent: "ml", refs: [".."] },
  { id: "scripts", name: "scripts", parent: "ops", acl: ["ops:cron"] },
  { id: "runbooks", name: "runbooks", parent: "shared", refs: ["../docs"] },
  { id: "docs", name: "docs", parent: "shared", acl: ["docs:edit"] },
  { id: "brand", name: "brand", parent: "eng", refs: ["../frontend"] },
  { id: "assets", name: "assets", parent: "brand", refs: ["../../frontend"] },
];

export const targetGrouping: TargetGrouping = {
  mustBeUnder: {
    ml: "eng",
    datasets: "eng",
    runbooks: "ops",
    brand: "shared",
    assets: "shared",
  },
};
