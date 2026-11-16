"use client";

import { useI18n } from "@/i18n";
import { getIntlLocale } from "@/i18n/runtime";
import * as React from "react";

import Link from "next/link";

import {
  ArrowLeftIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  GaugeIcon,
  Loader2Icon,
  MapPinIcon,
  RotateCwIcon,
  ShieldCheckIcon,
  UsersIcon,
} from "lucide-react";
import { toast } from "sonner";

import { StatusBadge } from "@/components/status-badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import {
  NOW_POLL_MS,
  NOW_TASK_LIMIT,
  nowCooldownRemaining,
  nowHasMoreTasks,
  nowResidueHref,
  nowSafetyBadges,
  nowSignature,
  nowVisibleTasks,
} from "@/lib/now-board";
import type { NowBoard, NowEgress, NowExit, NowHost, NowTask } from "@/lib/types";
import { toneClasses } from "@/lib/status";
import { cn } from "@/lib/utils";

function count(n: number): string {
  return n.toLocaleString(getIntlLocale());
}

// ModeBadge mirrors the task header: outline for the unchanged pentest default,
// filled for op so a stealth engagement is visible at a glance.
function ModeBadge({ mode }: { mode: string }) {
  const { t: swt } = useI18n();
  const op = mode === "op";
  let label = String(mode || "");
  if (op) label = swt("interface.m2891");
  else if (mode === "pentest" || !mode) label = swt("interface.m2890");
  return (
    <Badge variant={op ? "default" : "outline"} className="h-5 px-2 font-semibold text-[10px] uppercase tracking-wide">
      {label}
    </Badge>
  );
}

function PhaseBadge({ phase }: { phase: string }) {
  const { t: swt } = useI18n();
  let label = phase;
  if (phase === "passive") label = swt("app.nowPhasePassive");
  else if (phase === "active" || !phase) label = swt("app.nowPhaseActive");
  else if (phase === "persisted") label = swt("app.nowPhasePersisted");
  return (
    <Badge variant="outline" className="h-5 px-2 font-semibold text-[10px] uppercase tracking-wide">
      {label}
    </Badge>
  );
}

function CooldownCell({ until, nowSec }: { until: number; nowSec: number }) {
  const { t: swt } = useI18n();
  const seconds = nowCooldownRemaining(until, nowSec);
  if (seconds <= 0) return <span className="text-muted-foreground">{swt("app.nowReady")}</span>;
  return <span className="font-medium text-amber-600 tabular-nums dark:text-amber-400">{swt("app.nowCooldown", { seconds })}</span>;
}

function BudgetHostTable({ hosts, nowSec }: { hosts: NowHost[]; nowSec: number }) {
  const { t: swt } = useI18n();
  if (hosts.length === 0) return <p className="px-3 py-4 text-xs text-muted-foreground">{swt("app.nowBudgetEmpty")}</p>;
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{swt("app.nowHost")}</TableHead>
          <TableHead className="text-right">{swt("app.nowRequests")}</TableHead>
          <TableHead className="text-right">429</TableHead>
          <TableHead className="text-right">403</TableHead>
          <TableHead className="text-right">{swt("app.nowLastStatus")}</TableHead>
          <TableHead className="text-right">{swt("app.nowCooldownHeader")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {hosts.map((host) => (
          <TableRow key={host.host}>
            <TableCell className="max-w-[18rem] truncate font-mono text-xs" title={host.host}>
              {host.host}
            </TableCell>
            <TableCell className="text-right tabular-nums">{count(host.requests)}</TableCell>
            <TableCell className={cn("text-right tabular-nums", host.s429 > 0 && "font-medium text-amber-600 dark:text-amber-400")}>
              {count(host.s429)}
            </TableCell>
            <TableCell className={cn("text-right tabular-nums", (host.s403 ?? 0) > 0 && "font-medium text-amber-600 dark:text-amber-400")}>
              {count(host.s403 ?? 0)}
            </TableCell>
            <TableCell className="text-right font-mono text-xs text-muted-foreground">{host.last_status || "—"}</TableCell>
            <TableCell className="text-right text-xs">
              <CooldownCell until={host.cooldown_until} nowSec={nowSec} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function TaskHostTable({ hosts, nowSec }: { hosts: NowHost[]; nowSec: number }) {
  const { t: swt } = useI18n();
  if (hosts.length === 0) return <p className="px-3 py-4 text-xs text-muted-foreground">{swt("app.nowNoHosts")}</p>;
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>{swt("app.nowHost")}</TableHead>
          <TableHead className="text-right">{swt("app.nowWorkersHeader")}</TableHead>
          <TableHead className="text-right">{swt("app.nowLeases")}</TableHead>
          <TableHead className="text-right">{swt("app.nowRequests")}</TableHead>
          <TableHead className="text-right">429</TableHead>
          <TableHead className="text-right">{swt("app.nowCooldownHeader")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {hosts.map((host) => (
          <TableRow key={host.host}>
            <TableCell className="max-w-[18rem] truncate font-mono text-xs" title={host.host}>
              {host.host}
            </TableCell>
            <TableCell className={cn("text-right tabular-nums", (host.workers ?? 0) === 0 && "text-muted-foreground")}>
              {count(host.workers ?? 0)}
            </TableCell>
            <TableCell className="text-right tabular-nums">{count(host.leases ?? 0)}</TableCell>
            <TableCell className="text-right tabular-nums">{count(host.requests)}</TableCell>
            <TableCell className={cn("text-right tabular-nums", host.s429 > 0 && "font-medium text-amber-600 dark:text-amber-400")}>
              {count(host.s429)}
            </TableCell>
            <TableCell className="text-right text-xs">
              <CooldownCell until={host.cooldown_until} nowSec={nowSec} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function EgressStrip({
  egress,
  onRotated,
}: {
  egress: NowEgress;
  onRotated: (next: NowExit) => void;
}) {
  const { t: swt } = useI18n();
  const [location, setLocation] = React.useState("");
  const [rotateError, setRotateError] = React.useState("");

  const safety = nowSafetyBadges(egress);
  const safe = safety.find((badge) => badge.id === "safe")?.ok ?? false;
  const badges = {
    connected: { label: egress.connected ? swt("app.nowConnected") : swt("app.nowDisconnected"), tone: egress.connected ? "green" : "red" },
    lockdown: { label: swt("app.nowLockdown"), tone: egress.lockdown ? "green" : "neutral" },
    safe: { label: safe ? swt("app.nowSafe") : swt("app.nowUnsafe"), tone: safe ? "green" : "red" },
  } as const;

  async function rotate() {
    setRotateError("");
    try {
      const next = await api.rotateEgress(location);
      const exit = (next.exit_ip ?? "").trim();
      toast.success(exit ? swt("app.nowRotateSuccess", { ip: exit }) : swt("app.nowRotateSuccessNoIP"));
      onRotated(next);
    } catch (e) {
      const message = swt("app.nowRotateFailed", { message: (e as Error).message });
      setRotateError(message);
      toast.error(message);
    }
  }

  return (
    <Card className="gap-3 py-4">
      <div className="flex flex-wrap items-center gap-2 px-4">
        <ShieldCheckIcon className="size-4 text-muted-foreground" />
        <h2 className="text-sm font-semibold">{swt("app.nowEgressTitle")}</h2>
        <Badge variant="outline" className="font-mono text-[10px] uppercase tracking-wide">
          {egress.driver || "—"}
        </Badge>
        {safety.map((badge) => {
          const meta = badges[badge.id];
          return (
            <Badge key={badge.id} variant="outline" className={cn("h-5 px-2 text-[10px] font-semibold uppercase tracking-wide", toneClasses[meta.tone])}>
              {meta.label}
            </Badge>
          );
        })}
        {egress.rotating && (
          <span className="inline-flex items-center gap-1 text-xs font-medium text-amber-600 dark:text-amber-400">
            <Loader2Icon className="size-3 animate-spin" />
            {swt("app.nowRotating")}
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-1 px-4 text-xs">
        <span className="text-muted-foreground">
          {swt("app.nowExitIP")}: <span className="font-mono text-foreground">{egress.exit_ip || "—"}</span>
        </span>
        <span className="inline-flex items-center gap-1 text-muted-foreground">
          <MapPinIcon className="size-3" />
          {swt("app.nowLocationLabel")}:{" "}
          <span className="text-foreground">{[egress.city, egress.country].filter(Boolean).join(", ") || "—"}</span>
        </span>
      </div>

      {egress.managed ? (
        <div className="flex flex-wrap items-center gap-2 px-4">
          <Input
            value={location}
            onChange={(event) => setLocation(event.target.value)}
            placeholder={swt("app.nowRotateLocationPlaceholder")}
            aria-label={swt("app.nowLocationLabel")}
            className="h-8 w-60"
          />
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button size="sm" variant="outline">
                <RotateCwIcon data-icon="inline-start" />
                {swt("app.nowRotate")}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{swt("app.nowRotateConfirmTitle")}</AlertDialogTitle>
                <AlertDialogDescription>{swt("app.nowRotateConfirmBody")}</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{swt("common.cancel")}</AlertDialogCancel>
                <AlertDialogAction onClick={() => void rotate()}>{swt("app.nowRotate")}</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      ) : (
        <p className="px-4 text-xs text-muted-foreground">{swt("app.nowRotateUnmanaged")}</p>
      )}

      <p className="px-4 text-xs text-muted-foreground">{swt("app.nowRotateNote")}</p>

      {rotateError && (
        <Alert variant="destructive" className="mx-4 w-auto">
          <CircleAlertIcon />
          <AlertDescription>{rotateError}</AlertDescription>
        </Alert>
      )}
    </Card>
  );
}

function BudgetStrip({ board, nowSec }: { board: NowBoard; nowSec: number }) {
  const { t: swt } = useI18n();
  const { rps, burst, hosts } = board.budget;
  return (
    <Card className="gap-2 py-4">
      <div className="flex flex-wrap items-center gap-2 px-4">
        <GaugeIcon className="size-4 text-muted-foreground" />
        <h2 className="text-sm font-semibold">{swt("app.nowBudgetTitle")}</h2>
        <Badge variant="outline" className="font-mono text-[10px]">
          {rps > 0 ? swt("app.nowBudgetRate", { rps, burst }) : swt("app.nowBudgetOff")}
        </Badge>
      </div>
      <BudgetHostTable hosts={hosts} nowSec={nowSec} />
    </Card>
  );
}

function TaskRow({ task, nowSec }: { task: NowTask; nowSec: number }) {
  const { t: swt } = useI18n();
  const [open, setOpen] = React.useState(false);
  const workers = task.workers > 0 ? swt("app.nowWorkers", { count: task.workers }) : swt("app.nowNoWorkers");

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <Collapsible open={open} onOpenChange={setOpen}>
        <div className="flex flex-wrap items-center gap-2 px-3 py-2.5">
          <Link
            href={`/function/tasks/detail?id=${encodeURIComponent(task.id)}`}
            className="min-w-0 flex-1 truncate text-sm font-medium hover:underline"
          >
            {task.name || swt("app.nowTaskFallback", { id: task.id })}
          </Link>
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{task.id}</code>
          <ModeBadge mode={String(task.mode)} />
          <PhaseBadge phase={String(task.phase)} />
          <StatusBadge domain="task" value={String(task.status)} dot />
          <span className={cn("inline-flex items-center gap-1 text-xs", task.workers > 0 ? "text-foreground" : "text-muted-foreground")}>
            <UsersIcon className="size-3" />
            {workers}
          </span>
          {task.residue_open > 0 ? (
            <Link
              href={nowResidueHref(task.id)}
              className="inline-flex h-5 items-center rounded-md border border-amber-500/20 bg-amber-500/15 px-2 text-[10px] font-semibold text-amber-600 dark:text-amber-400"
            >
              {swt("app.nowResidue", { count: task.residue_open })}
            </Link>
          ) : (
            <span className="text-xs text-muted-foreground">{swt("app.nowResidue", { count: 0 })}</span>
          )}
          <CollapsibleTrigger asChild>
            <Button variant="ghost" size="sm" className="h-7 gap-1 px-2 text-xs">
              <ChevronRightIcon className={cn("size-3.5 transition-transform", open && "rotate-90")} />
              {swt("app.nowHostsCount", { count: task.hosts.length })}
            </Button>
          </CollapsibleTrigger>
        </div>
        <CollapsibleContent>
          <div className="border-t">
            <TaskHostTable hosts={task.hosts} nowSec={nowSec} />
          </div>
        </CollapsibleContent>
      </Collapsible>
    </Card>
  );
}

export default function NowPage() {
  const { t: swt, locale: swLocale } = useI18n();

  const [board, setBoard] = React.useState<NowBoard | null>(null);
  const [loaded, setLoaded] = React.useState(false);
  const [error, setError] = React.useState("");
  const [nowSec, setNowSec] = React.useState(() => Math.floor(Date.now() / 1000));

  // lastRef holds the previous poll's serialized payload: the board is re-fetched
  // every NOW_POLL_MS but usually comes back unchanged, and setBoard on an
  // identical payload would re-render the whole surface for nothing.
  const lastRef = React.useRef<string>("");

  const load = React.useCallback((force = false) => {
    api
      .nowBoard()
      .then((next) => {
        const signature = nowSignature(next);
        if (!force && signature === lastRef.current) {
          setLoaded(true);
          return;
        }
        lastRef.current = signature;
        setBoard(next);
        setError("");
        setLoaded(true);
      })
      .catch((e) => {
        setError((e as Error).message || swt("app.nowLoadFailed"));
        setLoaded(true);
      });
  }, [swLocale, swt]);

  React.useEffect(() => {
    load();
    const timer = setInterval(load, NOW_POLL_MS);
    return () => clearInterval(timer);
  }, [load]);

  const hasCooldown = React.useMemo(() => {
    if (!board) return false;
    const hosts = [...board.budget.hosts, ...board.tasks.flatMap((task) => task.hosts)];
    return hosts.some((host) => nowCooldownRemaining(host.cooldown_until, nowSec) > 0);
  }, [board, nowSec]);

  React.useEffect(() => {
    if (!hasCooldown) return;
    const timer = setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(timer);
  }, [hasCooldown]);

  const retry = React.useCallback(() => {
    lastRef.current = "";
    setError("");
    setLoaded(false);
    load(true);
  }, [load]);

  const onRotated = React.useCallback((next: NowExit) => {
    setBoard((current) => {
      if (!current) return current;
      const exit_ip = (next.exit_ip ?? "").trim() || current.egress.exit_ip;
      const city = next.city !== undefined ? next.city : current.egress.city;
      const country = next.country !== undefined ? next.country : current.egress.country;
      const updated: NowBoard = {
        ...current,
        egress: {
          ...current.egress,
          connected: next.connected,
          lockdown: next.lockdown,
          exit_ip,
          city,
          country,
          safe: next.connected || next.lockdown,
          rotating: next.rotating,
        },
      };
      lastRef.current = nowSignature(updated);
      return updated;
    });
    setNowSec(Math.floor(Date.now() / 1000));
  }, []);

  const tasks = board ? nowVisibleTasks(board.tasks) : [];

  return (
    <div className="flex flex-1 flex-col">
      <header className="sticky top-0 z-10 flex items-center gap-2 border-b bg-background/95 px-4 py-2.5 backdrop-blur lg:px-6">
        <SidebarTrigger className="-ml-1" />
        <Button asChild variant="ghost" size="icon" className="size-7">
          <Link href="/function/tasks">
            <ArrowLeftIcon />
          </Link>
        </Button>
        <GaugeIcon className="size-4 text-muted-foreground" />
        <h1 className="min-w-0 flex-1 truncate text-sm font-semibold">{swt("app.nowTitle")}</h1>
        <Badge variant="outline" className="hidden font-mono text-[10px] sm:inline-flex">
          /api/now
        </Badge>
      </header>

      <div className="flex flex-1 flex-col gap-4 p-4 lg:p-6">
        <p className="text-xs text-muted-foreground">{swt("app.nowSubtitle")}</p>

        {error && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>{swt("app.nowLoadFailed")}</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center gap-2">
              <span className="min-w-0 flex-1 break-words">{error}</span>
              <Button size="sm" variant="outline" onClick={retry}>
                {swt("common.retry")}
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {!loaded && !board ? (
          <div className="flex flex-col gap-4">
            <p className="text-xs text-muted-foreground">{swt("app.nowLoading")}</p>
            <Skeleton className="h-32 w-full" />
            <Skeleton className="h-28 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : board ? (
          <>
            <EgressStrip egress={board.egress} onRotated={onRotated} />
            <BudgetStrip board={board} nowSec={nowSec} />

            <section className="flex flex-col gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-sm font-semibold">{swt("app.nowTasksTitle")}</h2>
                <Badge variant="outline" className="font-mono text-[10px] tabular-nums">
                  {count(board.tasks.length)}
                </Badge>
              </div>

              {nowHasMoreTasks(board.tasks) && (
                <p className="text-xs text-muted-foreground">{swt("app.nowTaskCap", { limit: NOW_TASK_LIMIT })}</p>
              )}

              {tasks.length === 0 ? (
                <Empty className="border border-dashed">
                  <EmptyHeader>
                    <EmptyTitle>{swt("app.nowEmptyTitle")}</EmptyTitle>
                    <EmptyDescription>{swt("app.nowEmptyDescription")}</EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : (
                <div className="flex flex-col gap-2">
                  {tasks.map((task) => (
                    <TaskRow key={task.id} task={task} nowSec={nowSec} />
                  ))}
                </div>
              )}
            </section>
          </>
        ) : null}
      </div>
    </div>
  );
}
