import { BuildSpec } from "../src/model";

/**
 * A demo build whose cache key forgets tool versions and a conditionally
 * read config file, and which hashes an env var that never matters.
 *
 * Expected audit findings:
 *  - artifact-affecting: tool:node, tool:terser, file:terser.config.json
 *  - log-only:           env:CI, env:VERBOSE
 *  - declared but ineffective: env:COLOR
 *  - never read:         env:LEGACY_FLAG
 */
export const spec: BuildSpec = {
  name: "demo-web-bundle",
  domain: {
    "env:NODE_ENV": ["development", "production"],
    "env:SOURCE_LOCALE": ["en", "zh"],
    "env:CI": ["false", "true"],
    "env:VERBOSE": ["0", "1"],
    "env:COLOR": ["0", "1"],
    "env:LEGACY_FLAG": ["0", "1"],
    "tool:node": ["18.19.0", "20.11.0"],
    "tool:terser": ["5.30.0", "5.31.0"],
    "file:terser.config.json": ["absent", "aggressive"],
  },
  keyInputs: ["env:NODE_ENV", "env:SOURCE_LOCALE", "env:COLOR"],
  build(env) {
    const mode = String(env["env:NODE_ENV"]);
    const locale = String(env["env:SOURCE_LOCALE"]);

    let artifact = `bundle(mode=${mode},locale=${locale})`;
    artifact += `|runtime=node-${env["tool:node"]}`;

    // Conditional tool use + conditional file read: production only.
    if (mode === "production") {
      const terser = env["tool:terser"];
      const config =
        "file:terser.config.json" in env && env["file:terser.config.json"] !== "absent"
          ? env["file:terser.config.json"]
          : "default";
      artifact += `|minify=terser-${terser}(${config})`;
    }

    let log = `[ci=${env["env:CI"]}]`;
    if (env["env:VERBOSE"] === "1") log += " <verbose trace>";
    log += ` built ${mode}/${locale}`;

    return { artifact, log };
  },
};
