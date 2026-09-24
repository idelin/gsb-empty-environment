/**
 * 运行样例：npm run demo
 * 演示三路合并的典型场景：自动合并、结构化冲突、CRLF 保留、UTF-16 偏移。
 */
import { merge3 } from "./src/merge.ts";

function show(title: string, text: string): void {
  console.log(`--- ${title} ---`);
  process.stdout.write(text.endsWith("\n") ? text : text + "\n");
  console.log();
}

// 场景 1：一侧改首行、另一侧改末行 → 自动合并（LF）
{
  const base = "apple\nbanana\ncherry\n";
  const ours = "APPLE\nbanana\ncherry\n";
  const theirs = "apple\nbanana\nCHERRY\n";
  const result = merge3(base, ours, theirs);
  console.log("=== 场景 1：首行/末行互不重叠修改（LF）===");
  console.log(`clean = ${result.clean}, conflicts = ${result.conflicts.length}`);
  show("合并结果", result.text);
}

// 场景 2：双方改同一行 → 结构化冲突
{
  const base = "title: demo\nversion: 1\nauthor: team\n";
  const ours = "title: demo\nversion: 2\nauthor: team\n";
  const theirs = "title: demo\nversion: 3\nauthor: team\n";
  const result = merge3(base, ours, theirs);
  console.log("=== 场景 2：同一行被双方改成不同值（冲突）===");
  console.log(`clean = ${result.clean}`);
  show("合并结果", result.text);
  for (const conflict of result.conflicts) {
    console.log("结构化冲突：", {
      startOffset: conflict.startOffset,
      endOffset: conflict.endOffset,
      base: conflict.base,
      ours: conflict.ours,
      theirs: conflict.theirs,
    });
  }
  console.log();
}

// 场景 3：CRLF 文件，双方同位置插入不同内容 → 冲突，换行风格保留
{
  const base = "header\r\nfooter\r\n";
  const ours = "header\r\nadded-by-ours\r\nfooter\r\n";
  const theirs = "header\r\nadded-by-theirs\r\nfooter\r\n";
  const result = merge3(base, ours, theirs);
  console.log("=== 场景 3：CRLF + 同位置插入不同内容（冲突）===");
  console.log(`clean = ${result.clean}, eol = ${JSON.stringify(result.eol)}`);
  console.log("合并结果（转义显示）：");
  console.log(JSON.stringify(result.text));
  console.log();
}

// 场景 4：含 emoji 的文本，验证 UTF-16 偏移不切开代理对
{
  const base = "intro 😀🎉\nmid\nend\n";
  const ours = "intro 😀🎉\nOURS\nend\n";
  const theirs = "intro 😀🎉\nTHEIRS\nend\n";
  const result = merge3(base, ours, theirs);
  const conflict = result.conflicts[0];
  console.log("=== 场景 4：emoji 文本的 UTF-16 冲突偏移 ===");
  console.log(`startOffset = ${conflict.startOffset}, endOffset = ${conflict.endOffset}`);
  console.log("冲突块文本：", JSON.stringify(result.text.slice(conflict.startOffset, conflict.endOffset)));
}
