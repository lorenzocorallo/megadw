import { createFileRoute } from "@tanstack/react-router";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { listDownloads, pauseDownload, resumeDownload } from "@/api/client";

function DownloadsPage() {
  const { t } = useTranslation();
  const downloads = useQuery({ queryKey: ["downloads"], queryFn: listDownloads });
  const client = useQueryClient();
  const resume = useMutation({
    mutationFn: resumeDownload,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["downloads"] }),
  });
  const pause = useMutation({
    mutationFn: pauseDownload,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["downloads"] }),
  });
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
          <div
            className="grid grid-cols-[minmax(0,1fr)_10rem_7rem] gap-4 border-b border-slate-800 px-5 py-4 last:border-b-0"
            key={download.id}
          >
            <Link
              className="truncate text-sm text-slate-200 hover:underline"
              to="/downloads/$downloadId"
              params={{ downloadId: download.id }}
            >
              {download.displayName}
            </Link>
            <span className="text-sm text-slate-400">
              {t(`download.status.${download.state}`, {
                defaultValue: t("downloads.unknownStatus"),
              })}
            </span>
            {download.state === "waiting_quota" ||
            download.state === "paused" ||
            download.state === "failed" ? (
              <button
                className="text-xs text-emerald-300"
                onClick={() => resume.mutate(download.id)}
              >
                {t("download.resumeNow")}
              </button>
            ) : download.state === "downloading" || download.state === "queued" ? (
              <button className="text-xs text-amber-300" onClick={() => pause.mutate(download.id)}>
                {t("download.pause")}
              </button>
            ) : (
              <span />
            )}
          </div>
        ))}
      </div>
    </section>
  );
}

export const Route = createFileRoute("/downloads")({ component: DownloadsPage });
