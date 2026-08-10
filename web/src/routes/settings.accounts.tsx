import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { createAccount, deleteAccount, listAccounts, testAccount } from "@/api/client";

function AccountsPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: listAccounts });
  const [label, setLabel] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [defaultAccount, setDefaultAccount] = useState(false);
  const add = useMutation({
    mutationFn: () =>
      createAccount({ label, email, password, defaultForDownloads: defaultAccount }),
    onSuccess: () => {
      setLabel("");
      setEmail("");
      setPassword("");
      setDefaultAccount(false);
      setMessage(t("pages.accounts.saved"));
      void client.invalidateQueries({ queryKey: ["accounts"] });
    },
    onError: (e: Error) => setMessage(e.message),
  });
  const remove = useMutation({
    mutationFn: deleteAccount,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["accounts"] }),
  });
  const test = useMutation({
    mutationFn: testAccount,
    onSuccess: () => void client.invalidateQueries({ queryKey: ["accounts"] }),
    onError: (e: Error) => setMessage(e.message),
  });
  return (
    <section className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t("pages.accounts.title")}</h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.accounts.description")}</p>
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
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder={t("pages.accounts.label")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t("pages.accounts.email")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder={t("pages.accounts.password")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <button
          className="rounded bg-emerald-500 px-3 py-2 text-slate-950"
          disabled={add.isPending}
        >
          {t("pages.accounts.add")}
        </button>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={defaultAccount}
            onChange={(event) => setDefaultAccount(event.target.checked)}
          />
          {t("pages.accounts.default")}
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
              <th className="p-3">{t("pages.accounts.label")}</th>
              <th className="p-3">{t("pages.accounts.email")}</th>
              <th className="p-3">{t("pages.accounts.status")}</th>
              <th className="p-3">{t("pages.accounts.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {accounts.data?.map((account) => (
              <tr key={account.id} className="border-b border-slate-900">
                <td className="p-3">{account.label}</td>
                <td className="p-3">{account.email}</td>
                <td className="p-3">{account.status}</td>
                <td className="flex gap-2 p-3">
                  <button className="text-emerald-300" onClick={() => test.mutate(account.id)}>
                    {t("pages.accounts.test")}
                  </button>
                  <button className="text-rose-300" onClick={() => remove.mutate(account.id)}>
                    {t("pages.accounts.remove")}
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

export const Route = createFileRoute("/settings/accounts")({ component: AccountsPage });
