// Syntax-checks the browser JavaScript embedded into the Go binaries (there is
// no frontend build step that would catch errors): Master/Agent consoles,
// the shared UI library and the Guacamole extension helpers.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dirs = [
  "internal/master/web",
  "internal/agent/web",
  "internal/webui/assets",
  "guacamole-extension/src/main/resources/js",
];

let failed = false;
let checked = 0;
for (const dir of dirs) {
  for (const name of readdirSync(dir).filter((f) => f.endsWith(".js"))) {
    const file = join(dir, name);
    try {
      new Function(readFileSync(file, "utf8"));
      checked++;
      console.log(`${file}: ok`);
    } catch (err) {
      console.error(`${file}: ${err.message}`);
      failed = true;
    }
  }
}
if (checked === 0) {
  console.error("no JavaScript files found");
  failed = true;
}
process.exit(failed ? 1 : 0);
