import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { SettingsForm } from "@/components/settings-form";

function DownloadSettingsPage() {
  const { t } = useTranslation();
  return (
    <section className="mx-auto w-full max-w-5xl space-y-6">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight text-slate-100">
          {t("pages.downloadSettings.title")}
        </h1>
        <p className="mt-2 text-sm text-slate-400">{t("pages.downloadSettings.description")}</p>
      </div>
      <SettingsForm section="downloads" />
    </section>
  );
}

export const Route = createFileRoute("/settings/downloads")({ component: DownloadSettingsPage });
