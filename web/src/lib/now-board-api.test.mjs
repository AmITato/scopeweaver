import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

// Pin the /api/now transport and rotation body against the real client.
const source = fs.readFileSync(new URL("./api.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});

function client(payload = {}) {
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
            return { ok: true, status: 200, json: async () => payload };
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

const egress = {
  driver: "mullvad",
  managed: true,
  connected: true,
  lockdown: true,
  exit_ip: "203.0.113.9",
  city: "Stockholm",
  country: "SE",
  safe: true,
  rotating: false,
};

test("nowBoard reads /now and normalizes missing collections", async () => {
  const { api, calls } = client({
    egress,
    budget: { rps: 1, burst: 5 },
    tasks: [{ id: "16", name: "", mode: "op", phase: "active", status: "running", workers: 3, residue_open: 2 }],
  });
  const board = await api.nowBoard();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/api/now");
  assert.equal(board.egress.driver, "mullvad");
  assert.equal(board.egress.safe, true);
  assert.equal(board.budget.rps, 1);
  assert.equal(board.budget.burst, 5);
  assert.equal(board.budget.hosts.length, 0);
  assert.equal(board.tasks.length, 1);
  assert.equal(board.tasks[0].hosts.length, 0);
  assert.equal(board.tasks[0].residue_open, 2);
});

test("nowBoard keeps returned hosts in server order", async () => {
  const hosts = [
    { host: "example.com", requests: 12, s429: 0, s403: 0, last_status: 200, cooldown_until: 0 },
    { host: "api.example.com", workers: 2, leases: 1, requests: 8, s429: 1, cooldown_until: 99 },
  ];
  const { api } = client({
    egress,
    budget: { rps: 2, burst: 4, hosts: [hosts[0]] },
    tasks: [{ id: "1", name: "n", mode: "op", phase: "active", status: "running", workers: 2, residue_open: 0, hosts }],
  });
  const board = await api.nowBoard();
  assert.equal(board.budget.hosts.length, 1);
  assert.equal(board.budget.hosts[0].host, "example.com");
  assert.equal(board.tasks[0].hosts.length, 2);
  assert.equal(board.tasks[0].hosts[0].host, "example.com");
  assert.equal(board.tasks[0].hosts[1].host, "api.example.com");
});

test("rotateEgress posts a trimmed optional location", async () => {
  const { api, calls } = client({ ...egress, rotating: true });
  await api.rotateEgress("  de fra  ");
  assert.equal(calls[0].path, "/api/egress/rotate");
  assert.equal(calls[0].init.method, "POST");
  assert.deepEqual(JSON.parse(calls[0].init.body), { location: "de fra" });
});

test("rotateEgress omits the location as an empty string", async () => {
  const { api, calls } = client(egress);
  await api.rotateEgress();
  assert.deepEqual(JSON.parse(calls[0].init.body), { location: "" });
});
