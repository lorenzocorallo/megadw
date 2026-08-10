import { useEffect, useState } from "react";
import { ArrowLeft, Pause, Play, RotateCcw, Trash2, X } from "lucide-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  APIError,
  cancelDownload,
  deleteDownload,
  getDownload,
  listDownloadEvents,
  pauseDownload,
  resumeDownload,
  retryDownload,
} from "@/api/client";
import { ThroughputChart, type ThroughputPoint } from "@/components/throughput-chart";
import { useEventStream } from "@/hooks/use-events";
import { formatBytes, formatDate } from "@/lib/format";

function DownloadDetailPage() {
  const { t } = useTranslation();
  const { downloadId } = Route.useParams();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const stream = useEventStream();
  const download = useQuery({
    queryKey: ["downloads", downloadId],
    queryFn: () => getDownload(downloadId),
  });
  const events = useQuery({
    queryKey: ["download-events", downloadId],
    queryFn: () => listDownloadEvents(downloadId),
  });
  const [points, setPoints] = useState<ThroughputPoint[]>([]);
  const [message, setMessage] = useState("");
  useEffect(() => setPoints([]), [downloadId]);
  const action = useMutation({
    mutationFn: (kind: "pause" | "resume" | "retry" | "cancel") => {
      if (kind === "pause") return pauseDownload(downloadId);
      if (kind === "resume") return resumeDownload(downloadId);
      if (kind === "retry") return retryDownload(downloadId);
      return cancelDownload(downloadId);
    },
    onSuccess: (next) => {
      setMessage(t("download.actionComplete"));
      queryClient.setQueryData(["downloads", downloadId], next);
      void queryClient.invalidateQueries({ queryKey: ["downloads"] });
      void queryClient.invalidateQueries({ queryKey: ["download-events", downloadId] });
    },
    onError: (error: Error) =>
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed")),
  });
  const remove = useMutation({
    mutationFn: (deleteFiles: boolean) => deleteDownload(downloadId, deleteFiles),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["downloads"] });
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      void navigate({ to: "/downloads" });
    },
    onError: (error: Error) =>
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed")),
  });

  useEffect(() => {
    const event = stream.lastEvent;
    if (!event || event.jobId !== downloadId || event.name !== "speed.updated") return;
    const speed = event.data?.speedBytesPerSecond;
    if (typeof speed !== "number" || !Number.isFinite(speed)) return;
    const timestamp = event.timestamp ? Date.parse(event.timestamp) : Date.now();
    const now = Number.isFinite(timestamp) ? timestamp : Date.now();
    setPoints((current) =>
      [...current, { timestamp: now, bytesPerSecond: speed }]
        .filter((point) => point.timestamp >= now - 30 * 60 * 1000)
        .slice(-7200),
    );
  }, [downloadId, stream.lastEvent]);

  if (download.isPending) return <p className="text-sm text-slate-400">{t("common.loading")}</p>;
  if (download.isError || !download.data)
    return (
      <p className="text-sm text-amber-300" role="alert">
        {t("errors.downloadNotFound")}
      </p>
    );
  const item = download.data;
  const units = [
    t("units.bytes"),
    t("units.kibibytes"),
    t("units.mebibytes"),
    t("units.gibibytes"),
    t("units.tebibytes"),
  ];
  const percent =
    item.totalBytes > 0
      ? Math.min(100, Math.round((item.bytesCommitted / item.totalBytes) * 100))
      : item.state === "completed"
        ? 100
        : 0;
  const detailEvents = events.data ?? item.events ?? [];
  const destination = [item.completeRoot, item.destinationSubdirectory].filter(Boolean).join("/");

  return (
    <section className="mx-auto w-full max-w-6xl space-y-6">
      <Link
        className="inline-flex items-center gap-2 text-sm text-slate-400 hover:text-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
        to="/downloads"
      >
        <ArrowLeft aria-hidden="true" size={16} />
        {t("download.backToDownloads")}
      </Link>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-xs uppercase tracking-[0.18em] text-slate-500">
            {t("pages.downloadDetail.title")}
          </p>
          <h1 className="mt-2 truncate text-3xl font-semibold tracking-tight text-slate-100">
            {item.displayName}
          </h1>
          <p className="mt-2 text-sm text-slate-400">
            {t(`download.kind.${item.sourceKind}`)} · {formatBytes(item.totalBytes, units)}
          </p>
        </div>
        <StatusBadge state={item.state} />
      </header>
      {message ? (
        <p className="text-sm text-emerald-300" role="status">
          {message}
        </p>
      ) : null}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Metric label={t("downloads.progress")} value={`${percent}%`} />
        <Metric
          label={t("downloads.speed")}
          value={`${formatBytes(item.speedBytesPerSecond || 0, units)}/${t("units.second")}`}
        />
        <Metric label={t("downloads.eta")} value={formatEta(item.etaSeconds, t)} />
        <Metric label={t("downloads.files")} value={String(item.files.length)} />
      </div>
      <div className="flex flex-wrap gap-2">
        {item.state === "downloading" || item.state === "queued" ? (
          <ActionButton
            label={t("download.pause")}
            icon={<Pause aria-hidden="true" size={15} />}
            onClick={() => action.mutate("pause")}
            disabled={action.isPending}
          />
        ) : null}
        {item.state === "ready" ||
        item.state === "paused" ||
        item.state === "paused_recovery" ||
        item.state === "waiting_quota" ? (
          <ActionButton
            label={t("download.resumeNow")}
            icon={<Play aria-hidden="true" size={15} />}
            onClick={() => action.mutate("resume")}
            disabled={action.isPending}
          />
        ) : null}
        {item.state === "failed" ? (
          <ActionButton
            label={t("download.retry")}
            icon={<RotateCcw aria-hidden="true" size={15} />}
            onClick={() => action.mutate("retry")}
            disabled={action.isPending}
          />
        ) : null}
        {item.state !== "completed" && item.state !== "cancelled" ? (
          <ActionButton
            label={t("download.cancel")}
            icon={<X aria-hidden="true" size={15} />}
            onClick={() => action.mutate("cancel")}
            disabled={action.isPending}
          />
        ) : null}
        <ActionButton
          label={t("download.delete")}
          icon={<Trash2 aria-hidden="true" size={15} />}
          onClick={() => {
            const deleteFiles = item.state !== "completed";
            if (
              window.confirm(
                t(deleteFiles ? "download.confirmDeleteFiles" : "download.confirmDelete"),
              )
            )
              remove.mutate(deleteFiles);
          }}
          disabled={remove.isPending}
        />
      </div>

      {item.state === "waiting_quota" ? (
        <div
          className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 text-sm text-amber-200"
          role="status"
        >
          {t("download.quotaWaiting", {
            next: item.quotaNextRetryAt
              ? formatDate(item.quotaNextRetryAt)
              : t("download.nextRetryUnknown"),
          })}
        </div>
      ) : null}

      <section
        className="rounded-xl border border-slate-800 bg-slate-900/60 p-5"
        aria-labelledby="download-summary-heading"
      >
        <h2 id="download-summary-heading" className="text-lg font-semibold text-slate-100">
          {t("download.summary")}
        </h2>
        <dl className="mt-4 grid gap-4 text-sm sm:grid-cols-2">
          <Info label={t("download.sourceType")} value={t(`download.kind.${item.sourceKind}`)} />
          <Info label={t("download.destination")} value={destination} />
          <Info
            label={t("download.account")}
            value={item.accountLabel || t("download.anonymous")}
          />
          <Info label={t("download.proxy")} value={item.proxyLabel || t("download.direct")} />
        </dl>
      </section>

      <section
        className="rounded-xl border border-slate-800 bg-slate-900/60 p-5"
        aria-labelledby="throughput-heading"
      >
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h2 id="throughput-heading" className="text-lg font-semibold text-slate-100">
            {t("download.throughput")}
          </h2>
          <span className="text-xs text-slate-500">{t("download.lastThirtyMinutes")}</span>
        </div>
        <div className="mt-4">
          <ThroughputChart
            points={points}
            ariaLabel={t("download.throughputAria")}
            unavailableLabel={t("download.chartUnavailable")}
            emptyLabel={t("download.chartWaiting")}
          />
        </div>
      </section>

      <section
        className="rounded-xl border border-slate-800 bg-slate-900/60 p-5"
        aria-labelledby="files-heading"
      >
        <h2 id="files-heading" className="text-lg font-semibold text-slate-100">
          {t("download.filesTitle")}
        </h2>
        <div className="mt-4 overflow-x-auto">
          <table
            className="w-full min-w-[520px] text-left text-sm"
            aria-label={t("download.filesTableLabel")}
          >
            <thead className="text-xs uppercase tracking-wide text-slate-500">
              <tr>
                <th className="px-2 py-3" scope="col">
                  {t("download.fileName")}
                </th>
                <th className="px-2 py-3" scope="col">
                  {t("downloads.status")}
                </th>
                <th className="px-2 py-3" scope="col">
                  {t("downloads.progress")}
                </th>
              </tr>
            </thead>
            <tbody>
              {item.files.map((file) => {
                const filePercent =
                  file.sizeBytes > 0
                    ? Math.min(100, Math.round((file.bytesCommitted / file.sizeBytes) * 100))
                    : file.state === "completed"
                      ? 100
                      : 0;
                return (
                  <tr className="border-t border-slate-800" key={file.id}>
                    <td className="max-w-[26rem] truncate px-2 py-3 text-slate-300">
                      {file.finalRelativePath}
                    </td>
                    <td className="px-2 py-3">
                      <StatusBadge state={file.state} />
                    </td>
                    <td className="px-2 py-3 text-slate-400">
                      {filePercent}% · {formatBytes(file.bytesCommitted, units)} /{" "}
                      {formatBytes(file.sizeBytes, units)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <section
        className="rounded-xl border border-slate-800 bg-slate-900/60 p-5"
        aria-labelledby="events-heading"
      >
        <div className="flex items-baseline justify-between gap-3">
          <h2 id="events-heading" className="text-lg font-semibold text-slate-100">
            {t("download.eventsTitle")}
          </h2>
          <span className="text-xs text-slate-500">{t("download.eventsLimit")}</span>
        </div>
        {detailEvents.length === 0 ? (
          <p className="mt-4 text-sm text-slate-500">{t("download.noEvents")}</p>
        ) : (
          <ol className="mt-4 max-h-80 space-y-3 overflow-y-auto">
            {detailEvents
              .slice(-200)
              .reverse()
              .map((event) => (
                <li className="border-l-2 border-slate-700 pl-3" key={event.id}>
                  <div className="flex flex-wrap justify-between gap-2 text-xs text-slate-500">
                    <span>{event.kind}</span>
                    <time dateTime={event.createdAt}>{formatDate(event.createdAt)}</time>
                  </div>
                  <p className="mt-1 text-sm text-slate-300">{event.message}</p>
                </li>
              ))}
          </ol>
        )}
      </section>
    </section>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <p className="text-xs uppercase tracking-wide text-slate-500">{label}</p>
      <p className="mt-2 text-xl font-semibold text-slate-100">{value}</p>
    </div>
  );
}
function Info({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs uppercase tracking-wide text-slate-500">{label}</dt>
      <dd className="mt-1 break-all text-slate-300">{value}</dd>
    </div>
  );
}
function ActionButton({
  label,
  icon,
  onClick,
  disabled,
}: {
  label: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled: boolean;
}) {
  return (
    <button
      className="inline-flex items-center gap-2 rounded-lg border border-slate-700 px-3 py-2 text-sm text-slate-300 hover:border-slate-500 hover:text-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
      type="button"
      aria-label={label}
      onClick={onClick}
      disabled={disabled}
    >
      {icon}
      {label}
    </button>
  );
}
function StatusBadge({ state }: { state: string }) {
  const { t } = useTranslation();
  const waiting = state === "waiting_quota";
  const active = state === "downloading";
  return (
    <span
      className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-sm ${active ? "border-emerald-500/40 text-emerald-300" : waiting ? "border-amber-500/40 text-amber-300" : "border-slate-700 text-slate-300"}`}
    >
      <span
        className={`size-2 rounded-full ${active ? "bg-emerald-400" : waiting ? "bg-amber-400" : "bg-slate-500"}`}
        aria-hidden="true"
      />
      {t(`download.status.${state}`, { defaultValue: t("downloads.unknownStatus") })}
    </span>
  );
}
function formatEta(
  value: number | undefined,
  t: (key: string, options?: Record<string, unknown>) => string,
) {
  if (!value || value < 1) return t("downloads.etaUnknown");
  if (value >= 3600) return t("downloads.etaHours", { value: Math.ceil(value / 3600) });
  if (value >= 60) return t("downloads.etaMinutes", { value: Math.ceil(value / 60) });
  return t("downloads.etaSeconds", { value: Math.ceil(value) });
}

export const Route = createFileRoute("/downloads/$downloadId")({ component: DownloadDetailPage });
