import type { JsonValue } from "../src/json.ts";

/** Template exercising loops, nested loops, conditionals, functions, defaults. */
export const USERS_TEMPLATE: JsonValue = {
  title: "${app.title | upper}",
  users: {
    $for: "app.users",
    $as: "u",
    $body: {
      name: "${u.name}",
      tag: "${u.profile.tag | default('none')}",
      admin: { $if: "u.admin", $then: "yes", $else: "no" },
      hobbies: { $for: "u.hobbies", $as: "h", $body: "${h}" },
    },
  },
};

/** Fresh copy of the standard input data (callers mutate via transactions). */
export function usersData(): JsonValue {
  return {
    app: {
      title: "demo",
      users: [
        { name: "ann", admin: true, hobbies: ["a", "b"], profile: { tag: "x" } },
        { name: "bob", admin: false, hobbies: ["c"], profile: {} },
        { name: "cid", admin: null, hobbies: [], profile: { tag: null } },
      ],
    },
  };
}
