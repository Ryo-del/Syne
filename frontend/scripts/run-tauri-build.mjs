import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const userArgs = process.argv.slice(2);
const hasBundlesFlag = userArgs.includes("--bundles");
const args = ["build", ...userArgs];
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const frontendDir = path.resolve(__dirname, "..");
const tauriBin = process.platform === "win32"
  ? path.join(frontendDir, "node_modules", ".bin", "tauri.cmd")
  : path.join(frontendDir, "node_modules", ".bin", "tauri");

if (process.platform === "darwin" && !hasBundlesFlag) {
  args.push("--bundles", "app");
}

const result = spawnSync(tauriBin, args, {
  stdio: "inherit",
});

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}
