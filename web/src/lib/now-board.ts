import type { NowBoard, NowEgress, NowHost, NowTask } from "@/lib/types";

// The board polls /api/now on this interval. Keep it slow enough that a poll
// never competes with the engine for the database.
export const NOW_POLL_MS = 5000;
// GET /api/now returns at most this many tasks (server nowTaskLimit).
export const NOW_TASK_LIMIT = 50;

// nowSignature serializes a poll so an unchanged payload does not re-render.
// JSON.stringify is stable for these DTOs because every key is authored by the
// server in a fixed order and every collection is an array.
export function nowSignature(board: NowBoard): string {
  return JSON.stringify(board);
}

// nowEgressSafe mirrors egress.Status.Safe: a tunnel that is up, or lockdown
// blocking traffic while it is down, cannot leak the host IP.
export function nowEgressSafe(egress: NowEgress): boolean {
  return egress.connected || egress.lockdown;
}

export interface NowSafetyBadge {
  id: "connected" | "lockdown" | "safe";
  ok: boolean;
}

// nowSafetyBadges maps the strip to three explicit signals. safe is derived
// locally rather than trusted from the payload so a stale snapshot cannot claim
// safety after the tunnel drops.
export function nowSafetyBadges(egress: NowEgress): NowSafetyBadge[] {
  return [
    { id: "connected", ok: egress.connected },
    { id: "lockdown", ok: egress.lockdown },
    { id: "safe", ok: nowEgressSafe(egress) },
  ];
}

// nowCooldownRemaining returns whole seconds until a host may be contacted
// again. A missing or past timestamp means ready (0).
export function nowCooldownRemaining(cooldownUntil: number, nowSec: number): number {
  if (!cooldownUntil || cooldownUntil <= nowSec) return 0;
  return Math.ceil(cooldownUntil - nowSec);
}

export function nowVisibleTasks(tasks: NowTask[]): NowTask[] {
  return tasks.slice(0, NOW_TASK_LIMIT);
}

// The endpoint caps its own answer, so a longer array only appears if the limit
// changes. Keep the UI honest and slice anyway.
export function nowHasMoreTasks(tasks: NowTask[]): boolean {
  return tasks.length > NOW_TASK_LIMIT;
}

export function nowHostRequests(hosts: NowHost[]): number {
  return hosts.reduce((total, host) => total + host.requests, 0);
}

// The residue tab is tracked with the detail surface (docs/opsec-blend.md §12);
// link to the detail route and anchor so it can deep-link once that tab lands.
export function nowResidueHref(taskID: string): string {
  return `/function/tasks/detail?id=${encodeURIComponent(taskID)}#residue`;
}
