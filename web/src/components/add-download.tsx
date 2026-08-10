import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  APIError,
  createDownload,
  listAccounts,
  listProxies,
  resolveDownload,
  type ResolvedDownload,
} from "@/api/client";

function formatBytes(value: number, unitLabels: string[]) {
  if (value < 1024) return `${value} ${unitLabels[0]}`;
  const units = unitLabels.slice(1);
  let scaled = value;
  let unit = unitLabels[0];
  for (const next of units) {
    scaled /= 1024;
    unit = next;
    if (scaled < 1024) break;
  }
  return `${scaled.toFixed(scaled >= 10 ? 0 : 1)} ${unit}`;
}

export function AddDownload() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [url, setURL] = useState("");
  const [destination, setDestination] = useState("");
  const [startImmediately, setStartImmediately] = useState(true);
  const [accountId, setAccountId] = useState("");
  const [proxyId, setProxyId] = useState("");
  const [resolved, setResolved] = useState<ResolvedDownload | null>(null);
  const [message, setMessage] = useState("");
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: listAccounts });
  const proxies = useQuery({ queryKey: ["proxies"], queryFn: listProxies });

  const resolveMutation = useMutation({
    mutationFn: () => resolveDownload(url, accountId),
    onSuccess: (value) => {
      setResolved(value);
      setMessage("");
    },
    onError: (error: Error) => {
      setResolved(null);
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed"));
    },
  });
  const createMutation = useMutation({
    mutationFn: () =>
      createDownload({
        url,
        accountId,
        proxyId,
        destinationSubdirectory: destination,
        startImmediately,
      }),
    onSuccess: () => {
      setURL("");
      setDestination("");
      setResolved(null);
      setMessage(t("download.added"));
      void queryClient.invalidateQueries({ queryKey: ["downloads"] });
    },
    onError: (error: Error) => {
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed"));
    },
  });

  return (
    <section className="rounded-xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold text-slate-100">{t("download.add")}</h2>
          <p className="mt-1 text-sm text-slate-400">{t("download.addDescription")}</p>
        </div>
        <span className="rounded-full border border-slate-700 px-3 py-1 text-xs text-slate-400">
          {accountId
            ? accounts.data?.find((item) => item.id === accountId)?.label
            : t("download.anonymous")}
        </span>
      </div>
      <div className="space-y-4">
        <label className="block space-y-2">
          <span className="text-sm font-medium text-slate-300">{t("download.megaURL")}</span>
          <textarea
            className="min-h-24 w-full resize-y rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 placeholder:text-slate-600 focus:ring-2"
            value={url}
            onChange={(event) => setURL(event.target.value)}
            placeholder={t("download.megaURLPlaceholder")}
            spellCheck={false}
          />
        </label>
        <div className="grid gap-4 md:grid-cols-2">
          <label className="block space-y-2">
            <span className="text-sm font-medium text-slate-300">{t("download.account")}</span>
            <select
              className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-400"
              value={accountId}
              onChange={(event) => setAccountId(event.target.value)}
            >
              <option value="">{t("download.anonymous")}</option>
              {accounts.data?.map((account) => (
                <option key={account.id} value={account.id}>
                  {account.label} ({account.email})
                </option>
              ))}
            </select>
          </label>
          <label className="block space-y-2">
            <span className="text-sm font-medium text-slate-300">{t("download.proxy")}</span>
            <select
              className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-400"
              value={proxyId}
              onChange={(event) => setProxyId(event.target.value)}
            >
              <option value="">{t("download.direct")}</option>
              {proxies.data
                ?.filter((item) => item.enabled)
                .map((proxy) => (
                  <option key={proxy.id} value={proxy.id}>
                    {proxy.name} ({proxy.type})
                  </option>
                ))}
            </select>
          </label>
        </div>
        <div className="grid gap-4 md:grid-cols-[1fr_auto] md:items-end">
          <label className="block space-y-2">
            <span className="text-sm font-medium text-slate-300">
              {t("download.destinationSubdirectory")}
            </span>
            <input
              className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 placeholder:text-slate-600 focus:ring-2"
              value={destination}
              onChange={(event) => setDestination(event.target.value)}
              placeholder={t("download.destinationPlaceholder")}
            />
          </label>
          <button
            className="rounded-lg border border-emerald-500/60 bg-emerald-500/10 px-4 py-2 text-sm font-medium text-emerald-300 transition hover:bg-emerald-500/20 disabled:cursor-not-allowed disabled:opacity-50"
            type="button"
            onClick={() => resolveMutation.mutate()}
            disabled={!url.trim() || resolveMutation.isPending}
          >
            {resolveMutation.isPending ? t("download.resolving") : t("download.resolve")}
          </button>
        </div>
        <label className="flex items-center gap-2 text-sm text-slate-300">
          <input
            className="size-4 accent-emerald-500"
            type="checkbox"
            checked={startImmediately}
            onChange={(event) => setStartImmediately(event.target.checked)}
          />
          {t("download.startImmediately")}
        </label>
        {message ? (
          <p className="text-sm text-amber-300" role="status">
            {message}
          </p>
        ) : null}
        {resolved ? (
          <div
            className="space-y-4 rounded-lg border border-slate-800 bg-slate-950/70 p-4"
            data-testid="resolved-download"
          >
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div>
                <p className="font-medium text-slate-100">{resolved.displayName}</p>
                <p className="mt-1 text-sm text-slate-400">
                  {t("download.metadata", {
                    kind: t(`download.kind.${resolved.kind}`),
                    files: resolved.fileCount,
                    size: formatBytes(resolved.totalBytes, [
                      t("units.bytes"),
                      t("units.kibibytes"),
                      t("units.mebibytes"),
                      t("units.gibibytes"),
                      t("units.tebibytes"),
                    ]),
                  })}
                </p>
              </div>
              <button
                className="rounded-lg bg-emerald-500 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
                type="button"
                onClick={() => createMutation.mutate()}
                disabled={createMutation.isPending}
              >
                {createMutation.isPending ? t("download.adding") : t("download.addDownload")}
              </button>
            </div>
            <ul className="max-h-52 space-y-2 overflow-y-auto border-t border-slate-800 pt-3 text-sm text-slate-300">
              {resolved.files.map((file) => (
                <li className="flex justify-between gap-4" key={file.nodeId}>
                  <span className="truncate">{file.relativePath}</span>
                  <span className="shrink-0 text-slate-500">
                    {formatBytes(file.size, [
                      t("units.bytes"),
                      t("units.kibibytes"),
                      t("units.mebibytes"),
                      t("units.gibibytes"),
                      t("units.tebibytes"),
                    ])}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </section>
  );
}
