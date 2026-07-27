import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

import {
  API_EVENT_CONTRACT_VERSION,
  VERSION,
  buildTimeline as buildFromSource,
} from "../src/index.js";
import {
  buildTimeline as buildFromEsm,
} from "../dist/event-timeline.mjs";

const canonicalFixtureUrl = new URL("../fixtures/canonical-events.json", import.meta.url);
const paginationFixtureUrl = new URL("../fixtures/pagination-fragments.json", import.meta.url);
const canonicalSnapshotUrl = new URL("../snapshots/canonical-events.timeline.json", import.meta.url);
const paginationSnapshotUrl = new URL("../snapshots/pagination-fragments.timeline.json", import.meta.url);

const canonicalFixture = await readJson(canonicalFixtureUrl);
const paginationFixture = await readJson(paginationFixtureUrl);
const iifeSource = await readFile(
  new URL("../dist/event-timeline.iife.js", import.meta.url),
  "utf8",
);
const iifeContext = {};
vm.runInNewContext(iifeSource, iifeContext, {
  filename: "event-timeline.iife.js",
});
const iifeApi = iifeContext.AgentHubEventTimeline;

const canonicalActual = {
  schemaVersion: 1,
  packageVersion: VERSION,
  apiEventContractVersion: API_EVENT_CONTRACT_VERSION,
  scenarios: Object.fromEntries(
    canonicalFixture.scenarios.map(({ name, events }) => [
      name,
      buildFromSource(events),
    ]),
  ),
};

let replayedEvents = [];
const paginationStages = [];
for (const page of paginationFixture.pages) {
  replayedEvents = replayedEvents.concat(page);
  paginationStages.push(buildFromSource(replayedEvents));
}
const paginationActual = {
  schemaVersion: 1,
  packageVersion: VERSION,
  apiEventContractVersion: API_EVENT_CONTRACT_VERSION,
  stages: paginationStages,
};

if (process.env.UPDATE_SNAPSHOTS === "1") {
  await writeJson(canonicalSnapshotUrl, canonicalActual);
  await writeJson(paginationSnapshotUrl, paginationActual);
}

test("shared canonical fixtures match the checked-in snapshots", async () => {
  assert.deepEqual(canonicalActual, await readJson(canonicalSnapshotUrl));
  assert.equal(
    canonicalFixture.apiEventContractVersion,
    API_EVENT_CONTRACT_VERSION,
  );
  const kinds = new Set(
    Object.values(canonicalActual.scenarios)
      .flat()
      .map((item) => item.kind),
  );
  assert.deepEqual(
    [...kinds].sort(),
    ["approval", "error", "lifecycle", "message", "thinking", "tools", "unknown"],
  );
});

test("pagination fragment replay matches every checked-in projection stage", async () => {
  assert.deepEqual(paginationActual, await readJson(paginationSnapshotUrl));
  assert.equal(
    paginationFixture.apiEventContractVersion,
    API_EVENT_CONTRACT_VERSION,
  );
  assert.equal(paginationStages[0][1].calls[0].status, "running");
  assert.equal(paginationStages[1][1].calls[0].output, "pag");
  assert.equal(paginationStages[2][1].calls[0].status, "completed");
  assert.equal(paginationStages[2].at(-1).text, "Turn completed");
});

test("source, ESM, and browser IIFE project every fixture identically", () => {
  assert.equal(typeof iifeApi?.buildTimeline, "function");
  assert.equal(iifeApi.VERSION, VERSION);
  assert.equal(iifeApi.API_EVENT_CONTRACT_VERSION, API_EVENT_CONTRACT_VERSION);

  const fixtureSets = [
    ...canonicalFixture.scenarios.map(({ events }) => events),
    ...paginationFixture.pages.map((_, index, pages) =>
      pages.slice(0, index + 1).flat(),
    ),
  ];
  for (const events of fixtureSets) {
    const expected = buildFromSource(events);
    assert.deepEqual(buildFromEsm(events), expected);
    assert.deepEqual(normalize(iifeApi.buildTimeline(events)), expected);
  }
});

test("the IIFE runs without React, a DOM, or a module loader", () => {
  const context = {};
  vm.runInNewContext(iifeSource, context);
  assert.deepEqual(normalize(Object.keys(context)), ["AgentHubEventTimeline"]);
  assert.deepEqual(
    normalize(context.AgentHubEventTimeline.buildTimeline([])),
    [],
  );
});

test("manifest hashes identify the exact deterministic artifacts and inputs", async () => {
  const manifest = await readJson(
    new URL("../dist/manifest.json", import.meta.url),
  );
  assert.equal(manifest.package.version, VERSION);
  assert.equal(
    manifest.apiEventContractVersion,
    API_EVENT_CONTRACT_VERSION,
  );
  assert.equal(manifest.license.id, "BSD-3-Clause");
  assert.equal(manifest.build.command, "npm run build");
  assert.equal(manifest.build.deterministic, true);

  const paths = {
    "src/index.js": new URL("../src/index.js", import.meta.url),
    "package.json": new URL("../package.json", import.meta.url),
    "scripts/build.mjs": new URL("../scripts/build.mjs", import.meta.url),
  };
  for (const [name, url] of Object.entries(paths)) {
    assert.equal(
      manifest.source.inputs[name],
      sha256(await readFile(url)),
      `${name} input hash`,
    );
  }
  for (const name of ["event-timeline.mjs", "event-timeline.iife.js"]) {
    assert.equal(
      manifest.artifacts[name].sha256,
      sha256(await readFile(new URL(`../dist/${name}`, import.meta.url))),
      `${name} artifact hash`,
    );
  }
});

function normalize(value) {
  return JSON.parse(JSON.stringify(value));
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function readJson(url) {
  return JSON.parse(await readFile(url, "utf8"));
}

async function writeJson(url, value) {
  await writeFile(url, `${JSON.stringify(value, null, 2)}\n`);
}
