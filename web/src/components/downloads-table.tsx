import { useMemo, useState } from "react";
import { ArrowDownUp, Pause, Play, RotateCcw, Trash2, X } from "lucide-react";
import {
  createColumnHelper,
  createFilteredRowModel,
  createSortedRowModel,
  columnFilteringFeature,
  globalFilteringFeature,
  rowSortingFeature,
  tableFeatures,
  useTable,
} from "@tanstack/react-table";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import type { Download } from "@/api/client";
import type { SortingState } from "@tanstack/react-table";

type DownloadsTableProps = {
  data: Download[];
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onRetry: (id: string) => void;
  onCancel: (id: string) => void;
  onDelete: (download: Download) => void;
  busyId?: string;
};

const features = tableFeatures({
  columnFilteringFeature,
  globalFilteringFeature,
  filteredRowModel: createFilteredRowModel(),
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
});

const EMPTY_DOWNLOADS: Download[] = [];

export function DownloadsTable({
  data,
  onPause,
  onResume,
  onRetry,
  onCancel,
  onDelete,
  busyId,
}: DownloadsTableProps) {
  const { t } = useTranslation();
  const [sorting, setSorting] = useState<SortingState>([]);
  const [globalFilter, setGlobalFilter] = useState("");
  const [stateFilter, setStateFilter] = useState("all");
  const helper = createColumnHelper<typeof features, Download>();
  const columns = useMemo(
    () =>
      helper.columns([
        helper.accessor("displayName", {
          header: t("downloads.name"),
          cell: (info) => (
            <Link
              className="max-w-48 truncate text-slate-100 hover:text-emerald-300 hover:underline"
              to="/downloads/$downloadId"
              params={{ downloadId: info.row.original.id }}
            >
              {info.getValue()}
            </Link>
          ),
        }),
        helper.accessor("state", {
          header: t("downloads.status"),
          cell: (info) => <StatusBadge state={info.getValue()} />,
        }),
        helper.accessor("bytesCommitted", {
          header: t("downloads.progress"),
          enableSorting: false,
          cell: (info) => (
            <ProgressCell value={info.getValue()} total={info.row.original.totalBytes} />
          ),
        }),
        helper.accessor("totalBytes", {
          header: t("downloads.size"),
          cell: (info) => formatBytes(info.getValue(), t),
        }),
        helper.accessor("speedBytesPerSecond", {
          header: t("downloads.speed"),
          enableSorting: false,
          cell: (info) => formatSpeed(info.getValue(), t),
        }),
        helper.accessor("etaSeconds", {
          header: t("downloads.eta"),
          enableSorting: false,
          cell: (info) => formatEta(info.getValue(), t),
        }),
        helper.accessor((row) => row.files.length, {
          id: "files",
          header: t("downloads.files"),
        }),
        helper.accessor("accountLabel", {
          header: t("downloads.account"),
          enableSorting: false,
          cell: (info) => info.getValue() || t("download.anonymous"),
        }),
        helper.accessor("proxyLabel", {
          header: t("downloads.proxy"),
          enableSorting: false,
          cell: (info) => info.getValue() || t("download.direct"),
        }),
        helper.accessor("createdAt", {
          header: t("downloads.added"),
          cell: (info) => formatDate(info.getValue()),
        }),
        helper.display({
          id: "actions",
          header: t("downloads.actions"),
          cell: (info) => (
            <DownloadActions
              download={info.row.original}
              busy={busyId === info.row.original.id}
              onPause={onPause}
              onResume={onResume}
              onRetry={onRetry}
              onCancel={onCancel}
              onDelete={onDelete}
            />
          ),
        }),
      ]),
    [busyId, helper, onCancel, onDelete, onPause, onResume, onRetry, t],
  );
  const filteredData = useMemo(
    () =>
      stateFilter === "all" ? data : data.filter((download) => download.state === stateFilter),
    [data, stateFilter],
  );
  const table = useTable({
    features,
    columns,
    data: filteredData.length > 0 ? filteredData : EMPTY_DOWNLOADS,
    state: { sorting, globalFilter },
    onSortingChange: setSorting,
    onGlobalFilterChange: setGlobalFilter,
  });

  return (
    <div className="space-y-4">
      <label className="block max-w-sm">
        <span className="sr-only">{t("downloads.search")}</span>
        <input
          className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 placeholder:text-slate-500 focus:ring-2"
          value={globalFilter}
          onChange={(event) => setGlobalFilter(event.target.value)}
          placeholder={t("downloads.searchPlaceholder")}
          type="search"
        />
      </label>
      <label className="block max-w-sm">
        <span className="sr-only">{t("downloads.filterByState")}</span>
        <select
          className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 focus:ring-2"
          value={stateFilter}
          onChange={(event) => setStateFilter(event.target.value)}
          aria-label={t("downloads.filterByState")}
        >
          <option value="all">{t("downloads.allStatuses")}</option>
          {DOWNLOAD_STATES.map((state) => (
            <option key={state} value={state}>
              {t(`download.status.${state}`, { defaultValue: state })}
            </option>
          ))}
        </select>
      </label>
      <div className="hidden overflow-x-auto rounded-xl border border-slate-800 md:block">
        <table
          className="w-full min-w-[960px] text-left text-sm"
          aria-label={t("downloads.tableLabel")}
        >
          <thead className="bg-slate-900/80 text-xs uppercase tracking-wide text-slate-500">
            {table.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => (
                  <th
                    className="whitespace-nowrap px-4 py-3 font-medium"
                    key={header.id}
                    scope="col"
                  >
                    {header.isPlaceholder ? null : (
                      <button
                        className="inline-flex items-center gap-1 rounded px-1 py-1 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                        type="button"
                        onClick={
                          header.column.getCanSort()
                            ? header.column.getToggleSortingHandler()
                            : undefined
                        }
                        aria-label={
                          header.column.getCanSort()
                            ? t("downloads.sortBy", {
                                column: String(header.column.columnDef.header),
                              })
                            : undefined
                        }
                      >
                        <table.FlexRender header={header} />
                        {header.column.getCanSort() ? (
                          <ArrowDownUp aria-hidden="true" size={13} />
                        ) : null}
                      </button>
                    )}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.map((row) => (
              <tr className="border-t border-slate-800" key={row.id}>
                {row.getAllCells().map((cell) => (
                  <td
                    className="whitespace-nowrap px-4 py-3 align-middle text-slate-300"
                    key={cell.id}
                  >
                    <table.FlexRender cell={cell} />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="space-y-3 md:hidden">
        {table.getRowModel().rows.map((row) => {
          const download = row.original;
          return (
            <article
              className="rounded-xl border border-slate-800 bg-slate-900/60 p-4"
              key={row.id}
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="truncate font-medium text-slate-100">
                    <Link
                      className="hover:text-emerald-300 hover:underline"
                      to="/downloads/$downloadId"
                      params={{ downloadId: download.id }}
                    >
                      {download.displayName}
                    </Link>
                  </h2>
                  <p className="mt-1 text-xs text-slate-500">{formatDate(download.createdAt)}</p>
                </div>
                <StatusBadge state={download.state} />
              </div>
              <div className="mt-4 space-y-3">
                <ProgressCell value={download.bytesCommitted} total={download.totalBytes} />
                <div className="grid grid-cols-2 gap-3 text-xs text-slate-400">
                  <span>{formatBytes(download.totalBytes, t)}</span>
                  <span>{formatSpeed(download.speedBytesPerSecond, t)}</span>
                  <span>{formatEta(download.etaSeconds, t)}</span>
                  <span>{t("downloads.fileCount", { count: download.files.length })}</span>
                </div>
              </div>
              <div className="mt-4 flex flex-wrap gap-2">
                <DownloadActions
                  download={download}
                  busy={busyId === download.id}
                  onPause={onPause}
                  onResume={onResume}
                  onRetry={onRetry}
                  onCancel={onCancel}
                  onDelete={onDelete}
                />
              </div>
            </article>
          );
        })}
      </div>
      {table.getRowModel().rows.length === 0 ? (
        <p className="rounded-xl border border-dashed border-slate-700 px-4 py-8 text-center text-sm text-slate-500">
          {t("dashboard.emptyQueue")}
        </p>
      ) : null}
    </div>
  );
}

const DOWNLOAD_STATES = [
  "ready",
  "queued",
  "resolving",
  "downloading",
  "waiting_quota",
  "paused",
  "paused_recovery",
  "finalizing",
  "verifying",
  "moving",
  "completed",
  "failed",
  "cancelled",
] as const;

function ProgressCell({ value, total }: { value: number; total: number }) {
  const percent = total > 0 ? Math.min(100, Math.round((value / total) * 100)) : 0;
  return (
    <div className="min-w-32">
      <div className="flex items-center justify-between gap-2 text-xs text-slate-400">
        <span>{percent}%</span>
        <span>
          {value.toLocaleString()} / {total.toLocaleString()}
        </span>
      </div>
      <div
        className="mt-1 h-1.5 overflow-hidden rounded-full bg-slate-800"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
      >
        <div
          className="h-full rounded-full bg-emerald-400 transition-[width]"
          style={{ width: `${percent}%` }}
        />
      </div>
    </div>
  );
}

function StatusBadge({ state }: { state: string }) {
  const { t } = useTranslation();
  const label = t(`download.status.${state}`, { defaultValue: t("downloads.unknownStatus") });
  const active = state === "downloading";
  const waiting = state === "waiting_quota";
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-xs ${active ? "border-emerald-500/40 text-emerald-300" : waiting ? "border-amber-500/40 text-amber-300" : "border-slate-700 text-slate-400"}`}
    >
      <span
        aria-hidden="true"
        className={`size-1.5 rounded-full ${active ? "bg-emerald-400" : waiting ? "bg-amber-400" : "bg-slate-500"}`}
      />
      {label}
    </span>
  );
}

function DownloadActions({
  download,
  busy,
  onPause,
  onResume,
  onRetry,
  onCancel,
  onDelete,
}: {
  download: Download;
  busy: boolean;
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onRetry: (id: string) => void;
  onCancel: (id: string) => void;
  onDelete: (download: Download) => void;
}) {
  const { t } = useTranslation();
  const terminal = download.state === "completed" || download.state === "cancelled";
  return (
    <div className="flex flex-wrap gap-1">
      {download.state === "downloading" || download.state === "queued" ? (
        <ActionButton
          label={t("download.pause")}
          icon={<Pause aria-hidden="true" size={14} />}
          disabled={busy}
          onClick={() => onPause(download.id)}
        />
      ) : null}
      {download.state === "ready" ||
      download.state === "paused" ||
      download.state === "paused_recovery" ||
      download.state === "waiting_quota" ? (
        <ActionButton
          label={t("download.resumeNow")}
          icon={<Play aria-hidden="true" size={14} />}
          disabled={busy}
          onClick={() => onResume(download.id)}
        />
      ) : null}
      {download.state === "failed" ? (
        <ActionButton
          label={t("download.retry")}
          icon={<RotateCcw aria-hidden="true" size={14} />}
          disabled={busy}
          onClick={() => onRetry(download.id)}
        />
      ) : null}
      {!terminal ? (
        <ActionButton
          label={t("download.cancel")}
          icon={<X aria-hidden="true" size={14} />}
          disabled={busy}
          onClick={() => onCancel(download.id)}
        />
      ) : null}
      <ActionButton
        label={t("download.delete")}
        icon={<Trash2 aria-hidden="true" size={14} />}
        disabled={busy}
        onClick={() => onDelete(download)}
      />
    </div>
  );
}

function ActionButton({
  label,
  icon,
  disabled,
  onClick,
}: {
  label: string;
  icon: React.ReactNode;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      className="inline-flex items-center gap-1 rounded-md border border-slate-700 px-2 py-1 text-xs text-slate-300 hover:border-slate-500 hover:text-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
      type="button"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      {icon}
      <span className="sr-only sm:not-sr-only">{label}</span>
    </button>
  );
}

function formatBytes(value: number, t: (key: string) => string) {
  const units = [
    t("units.bytes"),
    t("units.kibibytes"),
    t("units.mebibytes"),
    t("units.gibibytes"),
    t("units.tebibytes"),
  ];
  if (value < 1024) return `${value} ${units[0]}`;
  let scaled = value;
  let unit = units[0];
  for (const next of units.slice(1)) {
    scaled /= 1024;
    unit = next;
    if (scaled < 1024) break;
  }
  return `${scaled.toFixed(scaled >= 10 ? 0 : 1)} ${unit}`;
}

function formatSpeed(value: number, t: (key: string) => string) {
  return value > 0 ? `${formatBytes(value, t)}/${t("units.second")}` : t("downloads.idle");
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

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "" : date.toLocaleString();
}
