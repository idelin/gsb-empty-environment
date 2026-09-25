import type { StatusNode } from "../src/nodeTree.ts";

/** Fixture status node tree (stands in for a DOM/accessibility subtree). */
export const fixtureStatusTree: StatusNode = {
  role: "status",
  children: [
    { role: "img", hidden: true, text: "decorative icon" },
    {
      role: "text",
      children: [
        { text: "上传进度" },
        { text: "42%" },
      ],
    },
    { role: "presentation", hidden: true, text: "spinner" },
  ],
};
