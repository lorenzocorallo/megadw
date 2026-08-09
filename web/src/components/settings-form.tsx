import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { APIError, getSettings, putSettings, type Settings } from "@/api/client";

export function SettingsForm({ section }: { section: "general" | "downloads" | "appearance" }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const settingsQuery = useQuery({ queryKey: ["settings"], queryFn: getSettings });
  const [value, setValue] = useState<Settings | null>(null);
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (settingsQuery.data) setValue(settingsQuery.data);
  }, [settingsQuery.data]);

  const saveMutation = useMutation({
    mutationFn: (next: Settings) => putSettings(next),
    onSuccess: (next) => {
      setValue(next);
      setMessage(t("settings.saved"));
      void queryClient.setQueryData(["settings"], next);
    },
    onError: (error: Error) => {
      setMessage(error instanceof APIError ? error.message : t("errors.requestFailed"));
    },
  });

  if (settingsQuery.isPending || value === null) {
    return <p className="text-sm text-slate-400">{t("common.loading")}</p>;
  }
  if (settingsQuery.isError) {
    return <p className="text-sm text-amber-300">{t("errors.requestFailed")}</p>;
  }

  const update = (change: (current: Settings) => Settings) => {
    setValue((current) => (current ? change(current) : current));
    setMessage("");
  };

  return (
    <form
      className="max-w-3xl space-y-6"
      onSubmit={(event) => {
        event.preventDefault();
        saveMutation.mutate(value);
      }}
    >
      {section === "general" ? (
        <>
          <Field label={t("settings.completeRoot")}>
            <input
              className={inputClass}
              value={value.paths.completeRoot}
              onChange={(event) =>
                update((current) => ({
                  ...current,
                  paths: { ...current.paths, completeRoot: event.target.value },
                }))
              }
            />
          </Field>
          <Field label={t("settings.incompleteRoot")}>
            <input
              className={inputClass}
              value={value.paths.incompleteRoot}
              onChange={(event) =>
                update((current) => ({
                  ...current,
                  paths: { ...current.paths, incompleteRoot: event.target.value },
                }))
              }
            />
          </Field>
          <Field label={t("settings.conflictPolicy")}>
            <select
              className={inputClass}
              value={value.downloads.conflictPolicy}
              onChange={(event) =>
                update((current) => ({
                  ...current,
                  downloads: {
                    ...current.downloads,
                    conflictPolicy: event.target.value as Settings["downloads"]["conflictPolicy"],
                  },
                }))
              }
            >
              <option value="rename">{t("settings.conflict.rename")}</option>
              <option value="overwrite">{t("settings.conflict.overwrite")}</option>
              <option value="fail">{t("settings.conflict.fail")}</option>
            </select>
          </Field>
          <label className="flex items-center gap-3 text-sm text-slate-300">
            <input
              className="size-4 accent-emerald-500"
              type="checkbox"
              checked={value.downloads.autoStart}
              onChange={(event) =>
                update((current) => ({
                  ...current,
                  downloads: { ...current.downloads, autoStart: event.target.checked },
                }))
              }
            />
            {t("settings.autoStart")}
          </label>
        </>
      ) : null}
      {section === "downloads" ? (
        <div className="grid gap-5 sm:grid-cols-2">
          <NumberField
            label={t("settings.segmentSizeBytes")}
            value={value.downloads.segmentSizeBytes}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, segmentSizeBytes: next },
              }))
            }
          />
          <NumberField
            label={t("settings.workersPerFile")}
            value={value.downloads.workersPerFile}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, workersPerFile: next },
              }))
            }
          />
          <NumberField
            label={t("settings.maxActiveFiles")}
            value={value.downloads.maxActiveFiles}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, maxActiveFiles: next },
              }))
            }
          />
          <NumberField
            label={t("settings.maxGlobalWorkers")}
            value={value.downloads.maxGlobalWorkers}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, maxGlobalWorkers: next },
              }))
            }
          />
          <NumberField
            label={t("settings.globalSpeedLimit")}
            value={value.downloads.globalSpeedLimitBytesPerSecond}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, globalSpeedLimitBytesPerSecond: next },
              }))
            }
          />
          <NumberField
            label={t("settings.checkpointInterval")}
            value={value.downloads.checkpointIntervalMs}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, checkpointIntervalMs: next },
              }))
            }
          />
          <NumberField
            label={t("settings.normalRetryLimit")}
            value={value.downloads.normalRetryLimit}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, normalRetryLimit: next },
              }))
            }
          />
        </div>
      ) : null}
      {section === "appearance" ? (
        <>
          <Field label={t("settings.theme")}>
            <select
              className={inputClass}
              value={value.ui.theme}
              onChange={(event) =>
                update((current) => ({
                  ...current,
                  ui: { ...current.ui, theme: event.target.value as Settings["ui"]["theme"] },
                }))
              }
            >
              <option value="system">{t("settings.themeSystem")}</option>
              <option value="light">{t("settings.themeLight")}</option>
              <option value="dark">{t("settings.themeDark")}</option>
            </select>
          </Field>
          <Field label={t("settings.language")}>
            <select className={inputClass} disabled value={value.ui.locale}>
              <option value="en">{t("settings.english")}</option>
            </select>
          </Field>
        </>
      ) : null}
      <div className="flex items-center gap-4">
        <button
          className="rounded-lg bg-emerald-500 px-4 py-2 text-sm font-semibold text-slate-950 transition hover:bg-emerald-400 disabled:opacity-50"
          disabled={saveMutation.isPending}
          type="submit"
        >
          {saveMutation.isPending ? t("settings.saving") : t("settings.save")}
        </button>
        {message ? (
          <span className="text-sm text-emerald-300" role="status">
            {message}
          </span>
        ) : null}
      </div>
    </form>
  );
}

const inputClass =
  "w-full rounded-lg border border-slate-700 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none ring-emerald-400 focus:ring-2";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-2">
      <span className="text-sm font-medium text-slate-300">{label}</span>
      {children}
    </label>
  );
}

function NumberField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) {
  return (
    <Field label={label}>
      <input
        className={inputClass}
        type="number"
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </Field>
  );
}
