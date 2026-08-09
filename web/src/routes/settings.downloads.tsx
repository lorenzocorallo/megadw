import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function DownloadSettingsPage() {
  return (
    <PagePlaceholder
      descriptionKey="pages.downloadSettings.description"
      titleKey="pages.downloadSettings.title"
    />
  );
}

export const Route = createFileRoute("/settings/downloads")({ component: DownloadSettingsPage });
