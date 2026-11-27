import { executeMerge, update } from "../src/index.js";
// This file is compile-only. It verifies that target and source may have
// different shapes while on/condition/assignment callbacks remain typed.
const result = executeMerge({
    target: [{ id: 1, name: "old", active: true }],
    source: [{ id: 1, newName: "new" }],
    on: (target, source) => target.id === source.id,
    whenMatched: [
        {
            condition: (_target, source) => source.newName.length > 0,
            action: update({
                name: (_target, source) => source.newName,
                active: true,
            }),
        },
    ],
});
void result;
//# sourceMappingURL=api-types.js.map