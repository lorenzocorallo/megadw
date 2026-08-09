import { createFileRoute } from "@tanstack/react-router";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

function SettingsPage() {
  const { t } = useTranslation();
  return (
    <section className="mx-auto w-full max-w-5xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.settings.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.settings.description")}</p>
      </div>
      <nav className="grid gap-3 sm:grid-cols-2">
        <SettingLink
          to="/settings/general"
          title={t("pages.general.title")}
          description={t("pages.general.description")}
        />
        <SettingLink
          to="/settings/downloads"
          title={t("pages.downloadSettings.title")}
          description={t("pages.downloadSettings.description")}
        />
        <SettingLink
          to="/settings/appearance"
          title={t("pages.appearance.title")}
          description={t("pages.appearance.description")}
        />
        <SettingLink
          to="/settings/accounts"
          title={t("pages.accounts.title")}
          description={t("pages.accounts.description")}
        />
        <SettingLink
          to="/settings/proxies"
          title={t("pages.proxies.title")}
          description={t("pages.proxies.description")}
        />
      </nav>
    </section>
  );
}

function SettingLink({
  to,
  title,
  description,
}: {
  to:
    | "/settings/general"
    | "/settings/downloads"
    | "/settings/appearance"
    | "/settings/accounts"
    | "/settings/proxies";
  title: string;
  description: string;
}) {
  return (
    <Link
      className="rounded-xl border border-slate-800 bg-slate-900/60 p-5 transition hover:border-slate-600"
      to={to}
    >
      <h2 className="font-medium text-slate-100">{title}</h2>
      <p className="mt-2 text-sm text-slate-400">{description}</p>
    </Link>
  );
}

export const Route = createFileRoute("/settings")({ component: SettingsPage });
