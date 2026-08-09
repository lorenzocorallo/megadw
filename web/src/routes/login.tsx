import { useState } from "react";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { APIError, login } from "@/api/client";

function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const mutation = useMutation({
    mutationFn: () => login(username, password),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["auth-status"] });
      void navigate({ to: "/" });
    },
    onError: (value: Error) =>
      setError(value instanceof APIError ? value.message : t("errors.requestFailed")),
  });
  return (
    <section className="mx-auto mt-10 w-full max-w-md rounded-xl border border-slate-800 bg-slate-900/60 p-6">
      <h1 className="text-2xl font-semibold text-slate-100">{t("auth.loginTitle")}</h1>
      <p className="mt-2 text-sm text-slate-400">{t("auth.loginDescription")}</p>
      <form
        className="mt-6 space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          setError("");
          mutation.mutate();
        }}
      >
        <label className="block space-y-2">
          <span className="text-sm text-slate-300">{t("auth.username")}</span>
          <input
            className={inputClass}
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </label>
        <label className="block space-y-2">
          <span className="text-sm text-slate-300">{t("auth.password")}</span>
          <input
            className={inputClass}
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        {error ? (
          <p className="text-sm text-amber-300" role="alert">
            {error}
          </p>
        ) : null}
        <button className={buttonClass} disabled={mutation.isPending} type="submit">
          {mutation.isPending ? t("auth.signingIn") : t("auth.login")}
        </button>
      </form>
    </section>
  );
}

const inputClass =
  "w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 focus:ring-2";
const buttonClass =
  "w-full rounded-lg bg-emerald-500 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-400 disabled:opacity-50";

export const Route = createFileRoute("/login")({ component: LoginPage });
