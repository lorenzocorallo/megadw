import { Pause, Play } from "lucide-react";
import { createFileRoute, Outlet, useLocation } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  APIError,
  cancelDownload,
  deleteDownload,
  listDownloads,
  pauseDownload,
  pauseQueue,
  resumeDownload,
  resumeQueue,
  retryDownload,
  type Download,
} from "@/api/client";
import { AddDownload } from "@/components/add-download";
import { DownloadsTable } from "@/components/downloads-table";

function DownloadsPage() {
  const { t } = useTranslation();
  const location = useLocation();
  const queryClient = useQueryClient();
  const [message, setMessage] = useState("");
  const downloads = useQuery({ queryKey: ["downloads"], queryFn: listDownloads });
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["downloads"] });
    void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
  };
  const action = useMutation({
    mutationFn: ({ kind, id }: { kind: "pause" | "resume" | "retry" | "cancel"; id: string }) => {
      if (kind === "pause") return pauseDownload(id);
      if (kind === "resume") return resumeDownload(id);
      if (kind === "retry") return retryDownload(id);
      return cancelDownload(id);
    },
    onSuccess: () => {
      setMessage(t("download.actionComplete"));
      invalidate();
    },
    onError: (error: Error) =>
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed")),
  });
  const remove = useMutation({
    mutationFn: ({ id, deleteFiles }: { id: string; deleteFiles: boolean }) =>
      deleteDownload(id, deleteFiles),
    onSuccess: () => {
      setMessage(t("download.deleted"));
      invalidate();
    },
    onError: (error: Error) =>
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed")),
  });
  const queueAction = useMutation({
    mutationFn: (kind: "pause" | "resume") => (kind === "pause" ? pauseQueue() : resumeQueue()),
    onSuccess: () => invalidate(),
    onError: (error: Error) =>
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed")),
  });
  const confirmDelete = (download: Download) => {
    const deleteFiles = download.state !== "completed";
    if (window.confirm(t(deleteFiles ? "download.confirmDeleteFiles" : "download.confirmDelete"))) {
      remove.mutate({ id: download.id, deleteFiles });
    }
  };

  if (location.pathname !== "/downloads") return <Outlet />;

  return (
    <section className="mx-auto w-full max-w-7xl space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
            {t("pages.downloads.title")}
          </h1>
          <p className="mt-2 text-sm text-slate-400">{t("pages.downloads.description")}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            className="inline-flex items-center gap-2 rounded-lg border border-slate-700 px-3 py-2 text-sm text-slate-300 hover:border-slate-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
            type="button"
            onClick={() => queueAction.mutate("pause")}
            disabled={queueAction.isPending}
          >
            <Pause aria-hidden="true" size={15} />
            {t("queue.pauseAll")}
          </button>
          <button
            className="inline-flex items-center gap-2 rounded-lg border border-emerald-500/50 px-3 py-2 text-sm text-emerald-300 hover:bg-emerald-500/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
            type="button"
            onClick={() => queueAction.mutate("resume")}
            disabled={queueAction.isPending}
          >
            <Play aria-hidden="true" size={15} />
            {t("queue.resumeAll")}
          </button>
          <AddDownload />
        </div>
      </header>
      {message ? (
        <p className="text-sm text-emerald-300" role="status">
          {message}
        </p>
      ) : null}
      {downloads.isPending ? <p className="text-sm text-slate-400">{t("common.loading")}</p> : null}
      {downloads.isError ? (
        <p className="text-sm text-amber-300" role="alert">
          {t("errors.requestFailed")}
        </p>
      ) : null}
      <DownloadsTable
        data={downloads.data ?? []}
        busyId={
          (action.isPending ? action.variables?.id : undefined) ??
          (remove.isPending ? remove.variables?.id : undefined)
        }
        onPause={(id) => action.mutate({ kind: "pause", id })}
        onResume={(id) => action.mutate({ kind: "resume", id })}
        onRetry={(id) => action.mutate({ kind: "retry", id })}
        onCancel={(id) => action.mutate({ kind: "cancel", id })}
        onDelete={confirmDelete}
      />
    </section>
  );
}

export const Route = createFileRoute("/downloads")({ component: DownloadsPage });
