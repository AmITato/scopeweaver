import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

// Load the real client with a stub transport so the opsec wire shape stays pinned.
const source = fs.readFileSync(new URL("./api.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});

function client() {
  const calls = [];
  const exports = {};
  const context = {
    exports,
    process: { env: {} },
    localStorage: { getItem: () => null },
    require(id) {
      if (id === "@/i18n/runtime") return { localizedUrl: (path) => path, translate: (key) => key };
      if (id === "@/i18n/request")
        return {
          localizedFetch: async (path, init) => {
            calls.push({ path, init });
            return { ok: true, status: 200, json: async () => ({}) };
          },
        };
      if (id === "@/lib/mock/enabled") return { MOCK: false };
      if (id === "@/lib/mock/handler") return { mockHandle() { throw new Error("Unexpected mock request"); } };
      throw new Error(`Unexpected import: ${id}`);
    },
  };
  context.window = { location: { protocol: "https:", hostname: "scope.example", port: "8797" } };
  vm.runInNewContext(outputText, context);
  return { api: exports.api, calls };
}

test("createTask keeps pentest defaults when mode is omitted", async () => {
  const { api, calls } = client();
  await api.createTask({ description: "d", goal: "g" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/api/tasks");
  const body = JSON.parse(calls[0].init.body);
  assert.equal(body.mode, "pentest");
  assert.equal(body.cover_story, "");
  assert.deepEqual(body.house_style, {});
  assert.deepEqual(body.stealth, {});
});

test("createTask sends the op profile, trimmed cover story and style objects", async () => {
  const { api, calls } = client();
  await api.createTask({
    description: "d",
    goal: "g",
    mode: "op",
    coverStory: "  contractor doing QuickBooks token integration  ",
    houseStyle: { branch: "^(DEV|TS)-[0-9]+" },
    stealth: { rps: 1 },
  });
  const body = JSON.parse(calls[0].init.body);
  assert.equal(body.mode, "op");
  assert.equal(body.cover_story, "contractor doing QuickBooks token integration");
  assert.deepEqual(body.house_style, { branch: "^(DEV|TS)-[0-9]+" });
  assert.deepEqual(body.stealth, { rps: 1 });
});

test("updateTaskOpsec PATCHes the opsec route and only sends provided fields", async () => {
  const { api, calls } = client();
  await api.updateTaskOpsec("t-42", { mode: "op", coverStory: "  facilities vendor  " });
  assert.equal(calls[0].path, "/api/tasks/t-42/opsec");
  assert.equal(calls[0].init.method, "PATCH");
  const body = JSON.parse(calls[0].init.body);
  assert.equal(body.mode, "op");
  assert.equal(body.cover_story, "facilities vendor");
  assert.equal("house_style" in body, false);
  assert.equal("stealth" in body, false);
});
