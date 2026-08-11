import { useEffect, useId, useRef, useState } from "react";
import { X } from "lucide-react";
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
import { formatBytes } from "@/lib/format";

export function AddDownload() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const titleID = useId();
  const dialogRef = useRef<HTMLElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const currentResolveSource = useRef("");
  const [open, setOpen] = useState(false);
  const [url, setURL] = useState("");
  const [destination, setDestination] = useState("");
  const [startImmediately, setStartImmediately] = useState(true);
  const [accountId, setAccountId] = useState("");
  const [proxyId, setProxyId] = useState("");
  const [defaultsApplied, setDefaultsApplied] = useState(false);
  const [resolved, setResolved] = useState<ResolvedDownload | null>(null);
  const [message, setMessage] = useState("");
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: listAccounts, enabled: open });
  const proxies = useQuery({ queryKey: ["proxies"], queryFn: listProxies, enabled: open });
  currentResolveSource.current = `${url}\u0000${accountId}\u0000${proxyId}`;

  useEffect(() => {
    if (!open || defaultsApplied || accounts.isPending || proxies.isPending) return;
    setAccountId(accounts.data?.find((account) => account.defaultForDownloads)?.id ?? "");
    setProxyId(proxies.data?.find((proxy) => proxy.enabled && proxy.defaultForDownloads)?.id ?? "");
    setResolved(null);
    setDefaultsApplied(true);
  }, [accounts.data, accounts.isPending, defaultsApplied, open, proxies.data, proxies.isPending]);

  useEffect(() => {
    if (!open) return;
    const focusable = () =>
      Array.from(
        dialogRef.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [href], [tabindex]:not([tabindex="-1"])',
        ) ?? [],
      );
    focusable()[0]?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setOpen(false);
        return;
      }
      if (event.key !== "Tab") return;
      const elements = focusable();
      if (elements.length === 0) return;
      const first = elements[0];
      const last = elements[elements.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      previousFocus.current?.focus();
      previousFocus.current = null;
    };
  }, [open]);

  const resolveMutation = useMutation({
    mutationFn: (source: { url: string; accountId: string; proxyId: string; key: string }) =>
      resolveDownload(source.url, source.accountId, source.proxyId),
    onSuccess: (value, source) => {
      if (source.key !== currentResolveSource.current) return;
      setResolved(value);
      setMessage("");
    },
    onError: (error: Error, source) => {
      if (source.key !== currentResolveSource.current) return;
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
      setMessage("");
      setAccountId("");
      setProxyId("");
      setOpen(false);
      void queryClient.invalidateQueries({ queryKey: ["downloads"] });
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    },
    onError: (error: Error) => {
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed"));
    },
  });
  const units = [
    t("units.bytes"),
    t("units.kibibytes"),
    t("units.mebibytes"),
    t("units.gibibytes"),
    t("units.tebibytes"),
  ];

  return (
    <>
      <button
        className="inline-flex items-center gap-2 rounded-lg bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 hover:bg-emerald-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-300"
        type="button"
        data-testid="add-download"
        onClick={() => {
          previousFocus.current =
            document.activeElement instanceof HTMLElement ? document.activeElement : null;
          setOpen(true);
          setDefaultsApplied(false);
          setMessage("");
        }}
      >
        <span aria-hidden="true" className="text-lg leading-none">
          +
        </span>
        {t("download.add")}
      </button>
      {open ? (
        <div
          className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-slate-950/80 px-4 py-8"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) setOpen(false);
          }}
        >
          <section
            ref={dialogRef}
            className="w-full max-w-2xl rounded-xl border border-slate-700 bg-slate-900 p-5 shadow-2xl"
            role="dialog"
            aria-modal="true"
            aria-labelledby={titleID}
            tabIndex={-1}
          >
            <div className="flex items-start justify-between gap-4">
              <div>
                <h2 id={titleID} className="text-lg font-semibold text-slate-100">
                  {t("download.add")}
                </h2>
                <p className="mt-1 text-sm text-slate-400">{t("download.addDescription")}</p>
              </div>
              <button
                className="rounded-md p-2 text-slate-400 hover:bg-slate-800 hover:text-slate-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                type="button"
                aria-label={t("common.close")}
                onClick={() => setOpen(false)}
              >
                <X aria-hidden="true" size={18} />
              </button>
            </div>
            <div className="mt-5 space-y-4">
              <label className="block space-y-2" htmlFor="mega-url">
                <span className="text-sm font-medium text-slate-300">{t("download.megaURL")}</span>
                <textarea
                  id="mega-url"
                  className="min-h-24 w-full resize-y rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 placeholder:text-slate-600 focus:ring-2"
                  value={url}
                  onChange={(event) => {
                    setURL(event.target.value);
                    setResolved(null);
                  }}
                  placeholder={t("download.megaURLPlaceholder")}
                  spellCheck={false}
                  autoFocus
                />
              </label>
              <div className="grid gap-4 md:grid-cols-2">
                <label className="block space-y-2" htmlFor="download-account">
                  <span className="text-sm font-medium text-slate-300">
                    {t("download.account")}
                  </span>
                  <select
                    id="download-account"
                    className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-300 outline-none focus:ring-2 focus:ring-emerald-400"
                    value={accountId}
                    onChange={(event) => {
                      setAccountId(event.target.value);
                      setResolved(null);
                    }}
                  >
                    <option value="">{t("download.anonymous")}</option>
                    {accounts.data?.map((account) => (
                      <option key={account.id} value={account.id}>
                        {account.label} ({account.email})
                      </option>
                    ))}
                  </select>
                </label>
                <label className="block space-y-2" htmlFor="download-proxy">
                  <span className="text-sm font-medium text-slate-300">{t("download.proxy")}</span>
                  <select
                    id="download-proxy"
                    className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-300 outline-none focus:ring-2 focus:ring-emerald-400"
                    value={proxyId}
                    onChange={(event) => {
                      setProxyId(event.target.value);
                      setResolved(null);
                    }}
                  >
                    <option value="">{t("download.direct")}</option>
                    {proxies.data
                      ?.filter((item) => item.enabled)
                      .map((proxy) => (
                        <option key={proxy.id} value={proxy.id}>
                          {proxy.name} (
                          {t(`pages.proxies.types.${proxy.type}`, { defaultValue: proxy.type })})
                        </option>
                      ))}
                  </select>
                </label>
              </div>
              <label className="block space-y-2" htmlFor="destination-subdirectory">
                <span className="text-sm font-medium text-slate-300">
                  {t("download.destinationSubdirectory")}
                </span>
                <input
                  id="destination-subdirectory"
                  className="w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 placeholder:text-slate-600 focus:ring-2"
                  value={destination}
                  onChange={(event) => setDestination(event.target.value)}
                  placeholder={t("download.destinationPlaceholder")}
                />
              </label>
              <label
                className="flex items-center gap-2 text-sm text-slate-300"
                htmlFor="start-immediately"
              >
                <input
                  id="start-immediately"
                  className="size-4 accent-emerald-500"
                  type="checkbox"
                  checked={startImmediately}
                  onChange={(event) => setStartImmediately(event.target.checked)}
                />
                {t("download.startImmediately")}
              </label>
              <div className="flex flex-wrap justify-end gap-2">
                <button
                  className="rounded-lg border border-slate-700 px-4 py-2 text-sm text-slate-300 hover:border-slate-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                  type="button"
                  onClick={() => setOpen(false)}
                >
                  {t("common.close")}
                </button>
                <button
                  className="rounded-lg border border-emerald-500/60 bg-emerald-500/10 px-4 py-2 text-sm font-medium text-emerald-300 hover:bg-emerald-500/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400 disabled:cursor-not-allowed disabled:opacity-50"
                  type="button"
                  onClick={() =>
                    resolveMutation.mutate({
                      url,
                      accountId,
                      proxyId,
                      key: `${url}\u0000${accountId}\u0000${proxyId}`,
                    })
                  }
                  disabled={!url.trim() || resolveMutation.isPending}
                >
                  {resolveMutation.isPending ? t("download.resolving") : t("download.resolve")}
                </button>
              </div>
              {message ? (
                <p className="text-sm text-amber-300" role="alert">
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
                          size: formatBytes(resolved.totalBytes, units),
                        })}
                      </p>
                    </div>
                    <button
                      className="rounded-lg bg-emerald-400 px-4 py-2 text-sm font-semibold text-slate-950 hover:bg-emerald-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-300 disabled:cursor-not-allowed disabled:opacity-50"
                      type="button"
                      onClick={() => createMutation.mutate()}
                      disabled={createMutation.isPending}
                    >
                      {createMutation.isPending ? t("download.adding") : t("download.addDownload")}
                    </button>
                  </div>
                  {resolved.kind === "folder" ? (
                    <details open className="border-t border-slate-800 pt-3">
                      <summary className="cursor-pointer text-sm font-medium text-slate-300">
                        {t("download.fileTree")}
                      </summary>
                      <FileTree files={resolved.files} units={units} />
                    </details>
                  ) : (
                    <FileTree files={resolved.files} units={units} />
                  )}
                </div>
              ) : null}
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}

function FileTree({ files, units }: { files: ResolvedDownload["files"]; units: string[] }) {
  return (
    <ul className="mt-3 max-h-52 space-y-2 overflow-y-auto text-sm text-slate-300">
      {files.map((file) => (
        <li className="flex justify-between gap-4" key={file.nodeId}>
          <span className="truncate">{file.relativePath}</span>
          <span className="shrink-0 text-slate-500">{formatBytes(file.size, units)}</span>
        </li>
      ))}
    </ul>
  );
}
