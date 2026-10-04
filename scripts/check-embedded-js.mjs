// Syntax-checks the JavaScript embedded as Go raw strings in the Master and
// Agent web UIs (there is no frontend build step that would catch errors).
import { readFileSync } from "node:fs";

const targets = [
  ["internal/master/ui.go", "masterJS"],
  ["internal/agent/ui.go", "agentJS"],
];

let failed = false;
for (const [file, name] of targets) {
  const src = readFileSync(file, "utf8");
  const match = src.match(new RegExp("const " + name + " = `([\\s\\S]*?)`"));
  if (!match) {
    console.error(`${file}: const ${name} not found`);
    failed = true;
    continue;
  }
  try {
    new Function(match[1]);
    console.log(`${file}: ${name} ok`);
  } catch (err) {
    console.error(`${file}: ${name}: ${err.message}`);
    failed = true;
  }
}
process.exit(failed ? 1 : 0);
