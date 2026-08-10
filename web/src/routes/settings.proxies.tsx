import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createProxy,
  deleteProxy,
  listProxies,
  testProxy,
  updateProxy,
  type ProxyProfile,
} from "@/api/client";

function ProxiesPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const proxies = useQuery({ queryKey: ["proxies"], queryFn: listProxies });
  const [name, setName] = useState("");
  const [type, setType] = useState("http");
  const [host, setHost] = useState("");
  const [port, setPort] = useState("8080");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [defaultProxy, setDefaultProxy] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: () =>
      editingID
        ? updateProxy(editingID, {
            name,
            type,
            host,
            port: Number(port),
            username,
            password,
            timeoutSeconds: 15,
            enabled: true,
            defaultForDownloads: defaultProxy,
          })
        : createProxy({
            name,
            type,
            host,
            port: Number(port),
            username,
            password,
            timeoutSeconds: 15,
            enabled: true,
            defaultForDownloads: defaultProxy,
          }),
    onSuccess: () => {
      setMessage(t("pages.proxies.saved"));
      setDefaultProxy(false);
      setEditingID(null);
      setName("");
      setHost("");
      setPort("8080");
      setUsername("");
      setPassword("");
      void client.invalidateQueries({ queryKey: ["proxies"] });
    },
    onError: (e: Error) => setMessage(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteProxy,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["proxies"] }),
  });
  const test = useMutation({ mutationFn: testProxy, onError: (e: Error) => setMessage(e.message) });
  const edit = (proxy: ProxyProfile) => {
    setEditingID(proxy.id);
    setName(proxy.name);
    setType(proxy.type);
    setHost(proxy.host);
    setPort(String(proxy.port));
    setUsername(proxy.username ?? "");
    setPassword("");
    setDefaultProxy(proxy.defaultForDownloads);
    setMessage("");
  };
  const reset = () => {
    setEditingID(null);
    setName("");
    setHost("");
    setPort("8080");
    setUsername("");
    setPassword("");
    setDefaultProxy(false);
  };
  return (
    <section className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t("pages.proxies.title")}</h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.proxies.description")}</p>
      </div>
      <form
        className="grid gap-3 rounded-xl border border-slate-800 bg-slate-900/60 p-5 md:grid-cols-4"
        onSubmit={(e) => {
          e.preventDefault();
          add.mutate();
        }}
      >
        <input
          required
          value={name}
          onChange={(e) => setName(e.target.value)}
          aria-label={t("pages.proxies.name")}
          placeholder={t("pages.proxies.name")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          aria-label={t("pages.proxies.type")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        >
          <option value="http">{t("pages.proxies.types.http")}</option>
          <option value="https">{t("pages.proxies.types.https")}</option>
          <option value="socks5">{t("pages.proxies.types.socks5")}</option>
        </select>
        <input
          required
          value={host}
          onChange={(e) => setHost(e.target.value)}
          aria-label={t("pages.proxies.host")}
          placeholder={t("pages.proxies.host")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required
          type="number"
          value={port}
          onChange={(e) => setPort(e.target.value)}
          aria-label={t("pages.proxies.port")}
          placeholder={t("pages.proxies.port")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          aria-label={t("pages.proxies.username")}
          placeholder={t("pages.proxies.username")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-label={t("pages.proxies.password")}
          placeholder={t("pages.proxies.password")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <button
          className="rounded bg-emerald-500 px-3 py-2 text-slate-950"
          type="submit"
          disabled={add.isPending}
        >
          {editingID ? t("pages.proxies.save") : t("pages.proxies.add")}
        </button>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={defaultProxy}
            onChange={(event) => setDefaultProxy(event.target.checked)}
          />
          {t("pages.proxies.default")}
        </label>
        {editingID ? (
          <button
            className="text-left text-sm text-slate-400 underline"
            type="button"
            onClick={reset}
          >
            {t("pages.proxies.cancelEdit")}
          </button>
        ) : null}
      </form>
      {message && (
        <p role="status" className="text-sm text-amber-300">
          {message}
        </p>
      )}
      <div className="overflow-x-auto rounded-xl border border-slate-800">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-slate-800 text-slate-400">
              <th className="p-3" scope="col">
                {t("pages.proxies.name")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.proxies.type")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.proxies.endpoint")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.proxies.actions")}
              </th>
            </tr>
          </thead>
          <tbody>
            {proxies.data?.map((proxy) => (
              <tr key={proxy.id} className="border-b border-slate-900">
                <td className="p-3">{proxy.name}</td>
                <td className="p-3">
                  {t(`pages.proxies.types.${proxy.type}`, { defaultValue: proxy.type })}
                </td>
                <td className="p-3">
                  {proxy.host}:{proxy.port}
                </td>
                <td className="flex gap-2 p-3">
                  <button
                    className="text-emerald-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => test.mutate(proxy.id)}
                  >
                    {t("pages.proxies.test")}
                  </button>
                  <button
                    className="text-sky-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => edit(proxy)}
                  >
                    {t("pages.proxies.edit")}
                  </button>
                  <button
                    className="text-rose-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => {
                      if (window.confirm(t("pages.proxies.confirmRemove"))) remove.mutate(proxy.id);
                    }}
                  >
                    {t("pages.proxies.remove")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

export const Route = createFileRoute("/settings/proxies")({ component: ProxiesPage });
