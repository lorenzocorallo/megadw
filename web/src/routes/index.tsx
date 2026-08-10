import { Activity, HardDrive, ListVideo, Pause, Play, Radio, Gauge } from "lucide-react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getDashboard, listDownloads, pauseQueue, resumeQueue } from "@/api/client";
import { AddDownload } from "@/components/add-download";
import { useEventStream } from "@/hooks/use-events";
import { formatBytes } from "@/lib/format";

function DashboardPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const downloads = useQuery({ queryKey: ["downloads"], queryFn: listDownloads });
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: getDashboard });
  const stream = useEventStream();
  const queuePause = useMutation({
    mutationFn: pauseQueue,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
  });
  const queueResume = useMutation({
    mutationFn: resumeQueue,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["dashboard"] }),
  });
  const items = downloads.data ?? [];
  const active = items.filter((item) =>
    ["resolving", "downloading", "finalizing"].includes(item.state),
  );
  const queued = items.filter((item) => item.state === "ready" || item.state === "queued").length;
  const waiting = items.filter((item) => item.state === "waiting_quota").length;
  const speed =
    items.reduce((total, item) => total + (item.speedBytesPerSecond || 0), 0) ||
    dashboard.data?.currentSpeedBytesPerSecond ||
    0;
  const sessionBytes = stream.sessionBytes || dashboard.data?.bytesDownloadedThisSession || 0;
  const units = [
    t("units.bytes"),
    t("units.kibibytes"),
    t("units.mebibytes"),
    t("units.gibibytes"),
    t("units.tebibytes"),
  ];

  return (
    <div className="mx-auto w-full max-w-6xl space-y-8">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <p
            className="mb-2 inline-flex items-center gap-2 text-xs uppercase tracking-[0.18em] text-emerald-300"
            role="status"
            aria-live="polite"
          >
            <Radio aria-hidden="true" size={14} />
            {stream.status === "connected" ? t("dashboard.live") : t("dashboard.reconnecting")}
          </p>
          <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
            {t("pages.dashboard.title")}
          </h1>
          <p className="mt-2 max-w-2xl text-sm text-slate-400">
            {t("pages.dashboard.description")}
          </p>
        </div>
        <div className="flex gap-2">
          <button
            className="inline-flex items-center gap-2 rounded-lg border border-slate-700 px-3 py-2 text-sm text-slate-300 hover:border-slate-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
            type="button"
            onClick={() => (queuePause.isPending ? undefined : queuePause.mutate())}
            disabled={queuePause.isPending}
          >
            <Pause aria-hidden="true" size={16} />
            {t("queue.pauseAll")}
          </button>
          <button
            className="inline-flex items-center gap-2 rounded-lg bg-emerald-400 px-3 py-2 text-sm font-semibold text-slate-950 hover:bg-emerald-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-300"
            type="button"
            onClick={() => (queueResume.isPending ? undefined : queueResume.mutate())}
            disabled={queueResume.isPending}
          >
            <Play aria-hidden="true" size={16} />
            {t("queue.resumeAll")}
          </button>
        </div>
      </header>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <Metric
          icon={<Gauge aria-hidden="true" size={17} />}
          label={t("dashboard.currentSpeed")}
          value={`${formatBytes(speed, units)}/${t("units.second")}`}
        />
        <Metric
          icon={<Activity aria-hidden="true" size={17} />}
          label={t("dashboard.activeJobs")}
          value={String(active.length)}
        />
        <Metric
          icon={<ListVideo aria-hidden="true" size={17} />}
          label={t("dashboard.queuedJobs")}
          value={String(queued)}
        />
        <Metric
          icon={<Radio aria-hidden="true" size={17} />}
          label={t("dashboard.waitingQuota")}
          value={String(waiting)}
        />
        <Metric
          icon={<Gauge aria-hidden="true" size={17} />}
          label={t("dashboard.sessionBytes")}
          value={formatBytes(sessionBytes, units)}
        />
        <Metric
          icon={<HardDrive aria-hidden="true" size={17} />}
          label={t("dashboard.diskFree")}
          value={formatBytes(dashboard.data?.diskFreeBytes ?? 0, units)}
        />
      </div>

      <AddDownload />

      <section
        className="rounded-xl border border-slate-800 bg-slate-900/60 p-5"
        aria-labelledby="active-downloads-heading"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 id="active-downloads-heading" className="text-lg font-semibold text-slate-100">
            {t("dashboard.currentQueue")}
          </h2>
          <Link className="text-sm text-emerald-300 hover:underline" to="/downloads">
            {t("dashboard.viewAll")}
          </Link>
        </div>
        {downloads.isPending ? (
          <p className="mt-4 text-sm text-slate-400">{t("common.loading")}</p>
        ) : null}
        {active.length === 0 && !downloads.isPending ? (
          <p className="mt-4 text-sm text-slate-400">{t("dashboard.emptyQueue")}</p>
        ) : null}
        {active.length > 0 ? (
          <ul className="mt-4 divide-y divide-slate-800">
            {active.slice(0, 8).map((download) => {
              const percent =
                download.totalBytes > 0
                  ? Math.min(100, Math.round((download.bytesCommitted / download.totalBytes) * 100))
                  : 0;
              return (
                <li className="py-4" key={download.id}>
                  <div className="flex flex-wrap items-center justify-between gap-3">
                    <Link
                      className="min-w-0 truncate text-sm text-slate-200 hover:text-emerald-300 hover:underline"
                      to="/downloads/$downloadId"
                      params={{ downloadId: download.id }}
                    >
                      {download.displayName}
                    </Link>
                    <span className="text-xs text-slate-500">
                      {percent}% · {formatBytes(download.speedBytesPerSecond || 0, units)}/
                      {t("units.second")}
                    </span>
                  </div>
                  <div
                    className="mt-2 h-1.5 overflow-hidden rounded-full bg-slate-800"
                    role="progressbar"
                    aria-label={t("dashboard.progressFor", { name: download.displayName })}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={percent}
                  >
                    <div
                      className="h-full rounded-full bg-emerald-400"
                      style={{ width: `${percent}%` }}
                    />
                  </div>
                </li>
              );
            })}
          </ul>
        ) : null}
      </section>
    </div>
  );
}

function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <div className="flex items-center gap-2 text-slate-500">
        {icon}
        <p className="text-xs uppercase tracking-wide">{label}</p>
      </div>
      <p className="mt-3 text-2xl font-semibold text-slate-100">{value}</p>
    </div>
  );
}

export const Route = createFileRoute("/")({ component: DashboardPage });
