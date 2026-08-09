import { createFileRoute } from "@tanstack/react-router";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { listDownloads } from "@/api/client";

function DownloadsPage() {
  const { t } = useTranslation();
  const downloads = useQuery({ queryKey: ["downloads"], queryFn: listDownloads });
  return (
    <section className="mx-auto w-full max-w-6xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.downloads.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.downloads.description")}</p>
      </div>
      <div className="overflow-hidden rounded-xl border border-slate-800 bg-slate-900/60">
        <div className="grid grid-cols-[minmax(0,1fr)_10rem] gap-4 border-b border-slate-800 px-5 py-3 text-xs uppercase tracking-wide text-slate-500">
          <span>{t("downloads.name")}</span>
          <span>{t("downloads.status")}</span>
        </div>
        {downloads.isPending ? (
          <p className="px-5 py-6 text-sm text-slate-400">{t("common.loading")}</p>
        ) : null}
        {downloads.data?.length === 0 ? (
          <p className="px-5 py-6 text-sm text-slate-400">{t("dashboard.emptyQueue")}</p>
        ) : null}
        {downloads.data?.map((download) => (
          <Link
            className="grid grid-cols-[minmax(0,1fr)_10rem] gap-4 border-b border-slate-800 px-5 py-4 last:border-b-0 hover:bg-slate-800/40"
            key={download.id}
            to="/downloads/$downloadId"
            params={{ downloadId: download.id }}
          >
            <span className="truncate text-sm text-slate-200">{download.displayName}</span>
            <span className="text-sm text-slate-400">
              {t(`download.status.${download.state}`, {
                defaultValue: t("downloads.unknownStatus"),
              })}
            </span>
          </Link>
        ))}
      </div>
    </section>
  );
}

export const Route = createFileRoute("/downloads")({ component: DownloadsPage });
