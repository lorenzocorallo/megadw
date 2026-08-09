import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { SettingsForm } from "@/components/settings-form";

function GeneralSettingsPage() {
  const { t } = useTranslation();
  return (
    <section className="mx-auto w-full max-w-5xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.general.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.general.description")}</p>
      </div>
      <SettingsForm section="general" />
    </section>
  );
}

export const Route = createFileRoute("/settings/general")({ component: GeneralSettingsPage });
