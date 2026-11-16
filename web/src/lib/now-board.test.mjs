import assert from "node:assert/strict";
import fs from "node:fs";
import test from "node:test";
import vm from "node:vm";
import ts from "typescript";

// Load the DOM-free Now board helpers. Keeping this test at the helper boundary
// pins the poll guard and the safety mapping without a component framework.
const source = fs.readFileSync(new URL("./now-board.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
});

const exports = {};
vm.runInNewContext(outputText, { exports, module: { exports }, require: () => ({}) });
const {
  NOW_TASK_LIMIT,
  nowCooldownRemaining,
  nowEgressSafe,
  nowHasMoreTasks,
  nowHostRequests,
  nowResidueHref,
  nowSafetyBadges,
  nowSignature,
  nowVisibleTasks,
} = exports;

function board(overrides = {}) {
  return {
    egress: {
      driver: "mullvad",
      managed: true,
      connected: true,
      lockdown: true,
      exit_ip: "203.0.113.9",
      city: "Stockholm",
      country: "SE",
      safe: true,
      rotating: false,
      ...overrides.egress,
    },
    budget: {
      rps: 1,
      burst: 5,
      hosts: [
        { host: "example.com", requests: 12, s429: 0, s403: 0, last_status: 200, cooldown_until: 0 },
      ],
      ...overrides.budget,
    },
    tasks: [
      {
        id: "16",
        name: "Acme",
        mode: "op",
        phase: "active",
        status: "running",
        workers: 3,
        residue_open: 2,
        hosts: [
          { host: "example.com", workers: 2, leases: 1, requests: 12, s429: 0, cooldown_until: 0 },
        ],
      },
      ...(overrides.tasks ?? []),
    ],
  };
}

test("nowSignature is stable for an unchanged poll and changes with any field", () => {
  const first = board();
  const same = board();
  assert.equal(nowSignature(first), nowSignature(same));

  const rotated = board({ egress: { rotating: true } });
  assert.notEqual(nowSignature(first), nowSignature(rotated));

  const moreRequests = board();
  moreRequests.budget.hosts[0].requests = 13;
  assert.notEqual(nowSignature(first), nowSignature(moreRequests));
});

test("safety is false only when the tunnel is down and lockdown is off", () => {
  const unsafe = board({ egress: { connected: false, lockdown: false } }).egress;
  assert.equal(nowEgressSafe(unsafe), false);
  const badges = nowSafetyBadges(unsafe);
  assert.equal(badges.map((badge) => `${badge.id}:${badge.ok}`).join(","), "connected:false,lockdown:false,safe:false");

  for (const egress of [
    board({ egress: { connected: true, lockdown: false } }).egress,
    board({ egress: { connected: false, lockdown: true } }).egress,
  ]) {
    assert.equal(nowEgressSafe(egress), true);
    assert.equal(nowSafetyBadges(egress).find((badge) => badge.id === "safe").ok, true);
  }
});

test("cooldown countdown rounds up and reports ready at zero", () => {
  assert.equal(nowCooldownRemaining(0, 1_000), 0);
  assert.equal(nowCooldownRemaining(999, 1_000), 0);
  assert.equal(nowCooldownRemaining(1_000, 1_000), 0);
  assert.equal(nowCooldownRemaining(1_001, 1_000), 1);
  assert.equal(nowCooldownRemaining(1_042, 1_000), 42);
});

test("task rendering is capped at the endpoint limit and flags extras", () => {
  const tasks = Array.from({ length: NOW_TASK_LIMIT + 2 }, (_, index) => ({ id: String(index) }));
  assert.equal(nowVisibleTasks(tasks).length, NOW_TASK_LIMIT);
  assert.equal(nowHasMoreTasks(tasks), true);
  assert.equal(nowHasMoreTasks(tasks.slice(0, NOW_TASK_LIMIT)), false);
});

test("host helpers sum requests and link the residue anchor", () => {
  assert.equal(
    nowHostRequests([
      { host: "a.test", requests: 2, s429: 0, cooldown_until: 0 },
      { host: "b.test", requests: 3, s429: 1, cooldown_until: 0 },
    ]),
    5,
  );
  assert.equal(nowResidueHref("16"), "/function/tasks/detail?id=16#residue");
  assert.equal(nowResidueHref("a/b"), "/function/tasks/detail?id=a%2Fb#residue");
});
