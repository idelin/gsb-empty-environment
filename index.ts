import * as corp from "./src/corp.ts";
import * as publicSuffix from "./src/public-suffix.ts";

export * from "./src/corp.ts";
export * from "./src/public-suffix.ts";

export default Object.freeze({ ...corp, ...publicSuffix });
