import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function GeneralSettingsPage() {
  return (
    <PagePlaceholder descriptionKey="pages.general.description" titleKey="pages.general.title" />
  );
}

export const Route = createFileRoute("/settings/general")({ component: GeneralSettingsPage });
