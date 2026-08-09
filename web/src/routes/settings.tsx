import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function SettingsPage() {
  return (
    <PagePlaceholder descriptionKey="pages.settings.description" titleKey="pages.settings.title" />
  );
}

export const Route = createFileRoute("/settings")({ component: SettingsPage });
