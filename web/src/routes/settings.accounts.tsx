import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createAccount,
  deleteAccount,
  listAccounts,
  testAccount,
  updateAccount,
  type Account,
} from "@/api/client";

function AccountsPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const accounts = useQuery({ queryKey: ["accounts"], queryFn: listAccounts });
  const [label, setLabel] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [message, setMessage] = useState("");
  const [defaultAccount, setDefaultAccount] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);
  const add = useMutation({
    mutationFn: () =>
      editingID
        ? updateAccount(editingID, { label, email, password, defaultForDownloads: defaultAccount })
        : createAccount({ label, email, password, defaultForDownloads: defaultAccount }),
    onSuccess: () => {
      setLabel("");
      setEmail("");
      setPassword("");
      setDefaultAccount(false);
      setEditingID(null);
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
  const edit = (account: Account) => {
    setEditingID(account.id);
    setLabel(account.label);
    setEmail(account.email);
    setPassword("");
    setDefaultAccount(account.defaultForDownloads);
    setMessage("");
  };
  const reset = () => {
    setEditingID(null);
    setLabel("");
    setEmail("");
    setPassword("");
    setDefaultAccount(false);
  };
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
          aria-label={t("pages.accounts.label")}
          placeholder={t("pages.accounts.label")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          aria-label={t("pages.accounts.email")}
          placeholder={t("pages.accounts.email")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <input
          required={!editingID}
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-label={t("pages.accounts.password")}
          placeholder={t("pages.accounts.password")}
          className="rounded border border-slate-700 bg-slate-950 px-3 py-2"
        />
        <button
          className="rounded bg-emerald-500 px-3 py-2 text-slate-950"
          type="submit"
          disabled={add.isPending}
        >
          {editingID ? t("pages.accounts.save") : t("pages.accounts.add")}
        </button>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={defaultAccount}
            onChange={(event) => setDefaultAccount(event.target.checked)}
          />
          {t("pages.accounts.default")}
        </label>
        {editingID ? (
          <button
            className="text-left text-sm text-slate-400 underline"
            type="button"
            onClick={reset}
          >
            {t("pages.accounts.cancelEdit")}
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
                {t("pages.accounts.label")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.accounts.email")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.accounts.status")}
              </th>
              <th className="p-3" scope="col">
                {t("pages.accounts.actions")}
              </th>
            </tr>
          </thead>
          <tbody>
            {accounts.data?.map((account) => (
              <tr key={account.id} className="border-b border-slate-900">
                <td className="p-3">{account.label}</td>
                <td className="p-3">{account.email}</td>
                <td className="p-3">
                  {t(`pages.accounts.statuses.${account.status}`, { defaultValue: account.status })}
                </td>
                <td className="flex gap-2 p-3">
                  <button
                    className="text-emerald-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => test.mutate(account.id)}
                  >
                    {t("pages.accounts.test")}
                  </button>
                  <button
                    className="text-sky-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => edit(account)}
                  >
                    {t("pages.accounts.edit")}
                  </button>
                  <button
                    className="text-rose-300 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-400"
                    type="button"
                    onClick={() => {
                      if (window.confirm(t("pages.accounts.confirmRemove")))
                        remove.mutate(account.id);
                    }}
                  >
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
