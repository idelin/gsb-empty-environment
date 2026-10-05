// 编译脚本：优先使用本地 node_modules 的 typescript；
// 本机无网络时回退到 Cursor 内置的 TypeScript 编译器 API。
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");

function loadTypeScript() {
  const candidates = [
    path.join(root, "node_modules", "typescript"),
    process.env.TYPESCRIPT_PATH,
    "/Applications/Cursor.app/Contents/Resources/app/extensions/node_modules/typescript",
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch {
      // try next
    }
  }
  console.error("未找到 TypeScript 编译器，请先执行 npm install，或设置 TYPESCRIPT_PATH 指向 typescript 模块目录。");
  process.exit(1);
}

const ts = loadTypeScript();

const configPath = ts.findConfigFile(root, ts.sys.fileExists, "tsconfig.json");
const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, root);

const program = ts.createProgram(parsed.fileNames, parsed.options);
const emitResult = program.emit();
const diagnostics = ts.getPreEmitDiagnostics(program).concat(emitResult.diagnostics);

for (const diagnostic of diagnostics) {
  const message = ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n");
  if (diagnostic.file && diagnostic.start !== undefined) {
    const { line, character } = diagnostic.file.getLineAndCharacterOfPosition(diagnostic.start);
    console.error(`${diagnostic.file.fileName}:${line + 1}:${character + 1} - ${message}`);
  } else {
    console.error(message);
  }
}

if (diagnostics.length > 0 || emitResult.emitSkipped) {
  console.error("编译失败");
  process.exit(1);
}
console.log("编译成功 -> dist/");
