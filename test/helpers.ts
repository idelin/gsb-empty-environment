import * as fs from "fs";
import * as os from "os";
import * as path from "path";

export interface Fixture {
  /** Lexical root (may contain symlinked ancestors, e.g. /var on macOS). */
  root: string;
  /** Symlink-resolved root; resolved targets land under here. */
  realRoot: string;
  cleanup: () => void;
}

/**
 * Layout:
 *   declared/keep.txt
 *   declared/inner-link   -> keep.txt            (stays inside declared/)
 *   declared/escape-link  -> ../outside/secret.txt
 *   declared/dir-link     -> ../outside          (link to outside dir)
 *   declared/sub/deep.txt
 *   outside/secret.txt
 *   root-link             -> outside/secret.txt  (outside any declaration)
 *   loop-a -> loop-b, loop-b -> loop-a
 */
export function makeFixture(): Fixture {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "access-audit-"));
  const declared = path.join(root, "declared");
  const outside = path.join(root, "outside");
  fs.mkdirSync(path.join(declared, "sub"), { recursive: true });
  fs.mkdirSync(outside);
  fs.writeFileSync(path.join(declared, "keep.txt"), "keep");
  fs.writeFileSync(path.join(declared, "sub", "deep.txt"), "deep");
  fs.writeFileSync(path.join(outside, "secret.txt"), "secret");
  fs.symlinkSync("keep.txt", path.join(declared, "inner-link"));
  fs.symlinkSync(
    path.join("..", "outside", "secret.txt"),
    path.join(declared, "escape-link")
  );
  fs.symlinkSync(path.join("..", "outside"), path.join(declared, "dir-link"));
  fs.symlinkSync(
    path.join("outside", "secret.txt"),
    path.join(root, "root-link")
  );
  fs.symlinkSync("loop-b", path.join(root, "loop-a"));
  fs.symlinkSync("loop-a", path.join(root, "loop-b"));
  return {
    root,
    realRoot: fs.realpathSync(root),
    cleanup: () => fs.rmSync(root, { recursive: true, force: true }),
  };
}
