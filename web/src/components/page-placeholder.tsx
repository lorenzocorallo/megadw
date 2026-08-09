import { useTranslation } from "react-i18next";

type PagePlaceholderProps = {
  descriptionKey: string;
  titleKey: string;
};

export function PagePlaceholder({ descriptionKey, titleKey }: PagePlaceholderProps) {
  const { t } = useTranslation();

  return (
    <section className="mx-auto w-full max-w-5xl space-y-3">
      <h1 className="text-3xl font-semibold tracking-tight text-slate-100">{t(titleKey)}</h1>
      <p className="text-sm text-slate-400">{t(descriptionKey)}</p>
    </section>
  );
}
