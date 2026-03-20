import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const frontendDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(frontendDir, "..");
const binariesDir = path.join(frontendDir, "src-tauri", "binaries");
const goCacheDir = path.join(frontendDir, ".cache", "go-build");

function currentTargetTriple() {
  if (process.env.SIDECAR_TARGET_TRIPLE) {
    return process.env.SIDECAR_TARGET_TRIPLE.trim();
  }
  return execFileSync("rustc", ["--print", "host-tuple"], {
    encoding: "utf8",
  }).trim();
}

function goTargetFromRustTriple(targetTriple) {
  if (!targetTriple) {
    throw new Error("empty target triple");
  }

  const lower = targetTriple.toLowerCase();
  const goos = lower.includes("windows")
    ? "windows"
    : lower.includes("darwin")
      ? "darwin"
      : lower.includes("linux")
        ? "linux"
        : null;

  let goarch = null;
  if (lower.startsWith("x86_64")) {
    goarch = "amd64";
  } else if (lower.startsWith("aarch64")) {
    goarch = "arm64";
  } else if (lower.startsWith("armv7")) {
    goarch = "arm";
  }

  if (!goos || !goarch) {
    throw new Error(`unsupported target triple: ${targetTriple}`);
  }

  const extraEnv = {};
  if (goarch === "arm" && lower.startsWith("armv7")) {
    extraEnv.GOARM = "7";
  }

  return { goos, goarch, extraEnv };
}

function removeOldSidecars(dir) {
  if (!fs.existsSync(dir)) {
    return;
  }
  for (const entry of fs.readdirSync(dir)) {
    if (entry.startsWith("syne-ui-api-")) {
      fs.rmSync(path.join(dir, entry), { force: true });
    }
  }
}

const targetTriple = currentTargetTriple();
const { goos, goarch, extraEnv } = goTargetFromRustTriple(targetTriple);
const extension = goos === "windows" ? ".exe" : "";

fs.mkdirSync(binariesDir, { recursive: true });
fs.mkdirSync(goCacheDir, { recursive: true });
removeOldSidecars(binariesDir);

const outputPath = path.join(
  binariesDir,
  `syne-ui-api-${targetTriple}${extension}`,
);

const result = spawnSync(
  "go",
  ["build", "-o", outputPath, "./cmd/syne-ui-api"],
  {
    cwd: repoRoot,
    stdio: "inherit",
    env: {
      ...process.env,
      CGO_ENABLED: "0",
      GOCACHE: process.env.GOCACHE || goCacheDir,
      GOOS: goos,
      GOARCH: goarch,
      ...extraEnv,
    },
  },
);

if (result.status !== 0) {
  process.exit(result.status ?? 1);
}

console.log(`Built sidecar: ${outputPath}`);
