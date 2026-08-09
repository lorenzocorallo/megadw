import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { AddDownload } from "@/components/add-download";
import { useQuery } from "@tanstack/react-query";
import { getSettings, listDownloads } from "@/api/client";

function DashboardPage() {
  const { t } = useTranslation();
  const downloads = useQuery({ queryKey: ["downloads"], queryFn: listDownloads });
  const settings = useQuery({ queryKey: ["settings"], queryFn: getSettings });
  const active =
    downloads.data?.filter(
      (download) => download.state === "queued" || download.state === "downloading",
    ).length ?? 0;
  const waiting =
    downloads.data?.filter((download) => download.state === "waiting_quota").length ?? 0;

  return (
    <div className="mx-auto w-full max-w-6xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.dashboard.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.dashboard.description")}</p>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Metric label={t("dashboard.activeJobs")} value={active} />
        <Metric
          label={t("dashboard.queuedJobs")}
          value={downloads.data?.filter((download) => download.state === "queued").length ?? 0}
        />
        <Metric label={t("dashboard.waitingQuota")} value={waiting} />
      </div>
      <AddDownload />
      <section className="rounded-xl border border-slate-800 bg-slate-900/60 p-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-slate-100">{t("dashboard.currentQueue")}</h2>
          <span className="text-sm text-slate-500">
            {settings.data?.paths.completeRoot ?? t("common.loading")}
          </span>
        </div>
        {downloads.isPending ? (
          <p className="mt-4 text-sm text-slate-400">{t("common.loading")}</p>
        ) : null}
        {downloads.data?.length === 0 ? (
          <p className="mt-4 text-sm text-slate-400">{t("dashboard.emptyQueue")}</p>
        ) : null}
        {downloads.data && downloads.data.length > 0 ? (
          <ul className="mt-4 divide-y divide-slate-800">
            {downloads.data.slice(0, 8).map((download) => (
              <li
                className="flex flex-wrap items-center justify-between gap-3 py-3"
                key={download.id}
              >
                <span className="truncate text-sm text-slate-200">{download.displayName}</span>
                <span className="text-xs uppercase tracking-wide text-slate-500">
                  {t(`download.status.${download.state}`, {
                    defaultValue: t("downloads.unknownStatus"),
                  })}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </section>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <p className="text-xs uppercase tracking-wide text-slate-500">{label}</p>
      <p className="mt-2 text-2xl font-semibold text-slate-100">{value}</p>
    </div>
  );
}

export const Route = createFileRoute("/")({ component: DashboardPage });
