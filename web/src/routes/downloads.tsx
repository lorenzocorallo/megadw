import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function DownloadsPage() {
  return (
    <PagePlaceholder
      descriptionKey="pages.downloads.description"
      titleKey="pages.downloads.title"
    />
  );
}

export const Route = createFileRoute("/downloads")({ component: DownloadsPage });
