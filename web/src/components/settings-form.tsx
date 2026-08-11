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

  useEffect(() => {
    if (section !== "appearance" || !value) return;
    const theme = value.ui.theme;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark = theme === "dark" || (theme === "system" && media.matches);
      document.documentElement.classList.toggle("dark", dark);
      document.documentElement.dataset.theme = theme;
    };
    apply();
    if (theme !== "system") return;
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, [section, value]);

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

  if (settingsQuery.isError) {
    return (
      <p className="text-sm text-amber-300" role="alert">
        {t("errors.requestFailed")}
      </p>
    );
  }
  if (settingsQuery.isPending || value === null) {
    return <p className="text-sm text-slate-400">{t("common.loading")}</p>;
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
          {!value.paths.completeRoot || !value.paths.incompleteRoot ? (
            <p className="text-sm text-amber-300" role="alert">
              {t("settings.transferRootsRequired")}
            </p>
          ) : null}
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
            min={1_048_576}
            max={67_108_864}
            step={1_048_576}
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
            min={1}
            max={16}
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
            min={1}
            max={16}
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
            min={1}
            max={64}
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
            min={0}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, globalSpeedLimitBytesPerSecond: next },
              }))
            }
          />
          <NumberField
            label={t("settings.perJobSpeedLimit")}
            value={value.downloads.perJobDefaultLimitBytesPerSecond}
            min={0}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: {
                  ...current.downloads,
                  perJobDefaultLimitBytesPerSecond: next,
                },
              }))
            }
          />
          <NumberField
            label={t("settings.checkpointInterval")}
            value={value.downloads.checkpointIntervalMs}
            min={100}
            max={60_000}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, checkpointIntervalMs: next },
              }))
            }
          />
          <NumberField
            label={t("settings.checkpointBytes")}
            value={value.downloads.checkpointBytes}
            min={1_048_576}
            max={1_099_511_627_776}
            step={1_048_576}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, checkpointBytes: next },
              }))
            }
          />
          <NumberField
            label={t("settings.normalRetryLimit")}
            value={value.downloads.normalRetryLimit}
            min={0}
            max={20}
            onChange={(next) =>
              update((current) => ({
                ...current,
                downloads: { ...current.downloads, normalRetryLimit: next },
              }))
            }
          />
          <h2 className="col-span-full border-t border-slate-800 pt-5 text-base font-semibold text-slate-100">
            {t("settings.networkLimits")}
          </h2>
          <NumberField
            label={t("settings.connectTimeout")}
            value={value.network.connectTimeoutSeconds}
            min={1}
            max={300}
            onChange={(next) =>
              update((current) => ({
                ...current,
                network: { ...current.network, connectTimeoutSeconds: next },
              }))
            }
          />
          <NumberField
            label={t("settings.responseHeaderTimeout")}
            value={value.network.responseHeaderTimeoutSeconds}
            min={1}
            max={600}
            onChange={(next) =>
              update((current) => ({
                ...current,
                network: { ...current.network, responseHeaderTimeoutSeconds: next },
              }))
            }
          />
          <NumberField
            label={t("settings.readIdleTimeout")}
            value={value.network.readIdleTimeoutSeconds}
            min={1}
            max={3600}
            onChange={(next) =>
              update((current) => ({
                ...current,
                network: { ...current.network, readIdleTimeoutSeconds: next },
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
  min,
  max,
  step,
  onChange,
}: {
  label: string;
  value: number;
  min?: number;
  max?: number;
  step?: number;
  onChange: (value: number) => void;
}) {
  return (
    <Field label={label}>
      <input
        className={inputClass}
        type="number"
        value={value}
        min={min}
        max={max}
        step={step}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </Field>
  );
}
