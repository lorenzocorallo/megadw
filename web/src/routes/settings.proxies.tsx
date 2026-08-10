import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { createProxy, deleteProxy, listProxies, testProxy } from "@/api/client";

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
  const add = useMutation({
    mutationFn: () =>
      createProxy({
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
      void client.invalidateQueries({ queryKey: ["proxies"] });
    },
    onError: (e: Error) => setMessage(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteProxy,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["proxies"] }),
  });
  const test = useMutation({ mutationFn: testProxy, onError: (e: Error) => setMessage(e.message) });
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
          placeholder={t("pages.proxies.name")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <select
          value={type}
          onChange={(e) => setType(e.target.value)}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        >
          <option value="http">HTTP</option>
          <option value="https">HTTPS CONNECT</option>
          <option value="socks5">SOCKS5</option>
        </select>
        <input
          required
          value={host}
          onChange={(e) => setHost(e.target.value)}
          placeholder={t("pages.proxies.host")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required
          type="number"
          value={port}
          onChange={(e) => setPort(e.target.value)}
          placeholder={t("pages.proxies.port")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder={t("pages.proxies.username")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t("pages.proxies.password")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <button
          className="rounded bg-emerald-500 px-3 py-2 text-slate-950"
          disabled={add.isPending}
        >
          {t("pages.proxies.add")}
        </button>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={defaultProxy}
            onChange={(event) => setDefaultProxy(event.target.checked)}
          />
          {t("pages.proxies.default")}
        </label>
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
              <th className="p-3">{t("pages.proxies.name")}</th>
              <th className="p-3">{t("pages.proxies.type")}</th>
              <th className="p-3">{t("pages.proxies.endpoint")}</th>
              <th className="p-3">{t("pages.proxies.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {proxies.data?.map((proxy) => (
              <tr key={proxy.id} className="border-b border-slate-900">
                <td className="p-3">{proxy.name}</td>
                <td className="p-3">{proxy.type}</td>
                <td className="p-3">
                  {proxy.host}:{proxy.port}
                </td>
                <td className="flex gap-2 p-3">
                  <button className="text-emerald-300" onClick={() => test.mutate(proxy.id)}>
                    {t("pages.proxies.test")}
                  </button>
                  <button className="text-rose-300" onClick={() => remove.mutate(proxy.id)}>
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
