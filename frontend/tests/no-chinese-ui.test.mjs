import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

// Regression guard: the production Web UI must not ship any Chinese copy.
// Everything served to or bundled into the browser is scanned for CJK
// Unified Ideographs. User-generated content, agent output, and
// README.zh-CN.md are out of scope and not part of these directories.

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// Mirrors the manual audit command (with a UTF-8 locale, or ripgrep):
//   git grep -nP "[\x{4e00}-\x{9fff}]" -- frontend/src frontend/index.html frontend/worker frontend/scripts
const scanTargets = ["src", "index.html", "worker", "scripts"];

// CJK Unified Ideographs plus Extension A, written as escapes so this file
// itself stays free of Han characters.
const hanPattern = /[\u3400-\u4dbf\u4e00-\u9fff]/u;

async function collectFiles(target) {
  const absolute = path.join(frontendRoot, target);
  const entries = await readdir(absolute, { withFileTypes: true }).catch(() => null);
  if (!entries) return [target]; // plain file such as index.html
  const files = [];
  for (const entry of entries) {
    const child = path.join(target, entry.name);
    if (entry.isDirectory()) files.push(...await collectFiles(child));
    else if (entry.isFile()) files.push(child);
  }
  return files;
}

test("production Web UI contains no Chinese characters", async () => {
  const offenders = [];
  for (const target of scanTargets) {
    for (const file of await collectFiles(target)) {
      const content = await readFile(path.join(frontendRoot, file), "utf8");
      content.split("\n").forEach((line, index) => {
        if (hanPattern.test(line)) offenders.push(`${file}:${index + 1}: ${line.trim()}`);
      });
    }
  }
  assert.deepEqual(offenders, [], `Chinese characters found in production UI files:\n${offenders.join("\n")}`);
});
