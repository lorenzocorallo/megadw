import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiRequest, type Download } from "@/api/client";

function DownloadDetailPage() {
  const { t } = useTranslation();
  const { downloadId } = Route.useParams();
  const download = useQuery({
    queryKey: ["downloads", downloadId],
    queryFn: () => apiRequest<Download>(`/api/v1/downloads/${downloadId}`),
  });
  return (
    <section className="mx-auto w-full max-w-5xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.downloadDetail.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.downloadDetail.description")}</p>
      </div>
      {download.isPending ? <p className="text-sm text-slate-400">{t("common.loading")}</p> : null}
      {download.data ? (
        <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-lg font-semibold text-slate-100">{download.data.displayName}</h2>
            <span className="text-sm text-slate-400">
              {t(`download.status.${download.data.state}`, {
                defaultValue: t("downloads.unknownStatus"),
              })}
            </span>
          </div>
          <ul className="mt-4 divide-y divide-slate-800">
            {download.data.files.map((file) => (
              <li className="flex flex-wrap justify-between gap-4 py-3 text-sm" key={file.id}>
                <span className="truncate text-slate-300">{file.finalRelativePath}</span>
                <span className="text-slate-500">
                  {t(`download.status.${file.state}`, {
                    defaultValue: t("downloads.unknownStatus"),
                  })}
                </span>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}

export const Route = createFileRoute("/downloads/$downloadId")({ component: DownloadDetailPage });
