import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function DownloadDetailPage() {
  return (
    <PagePlaceholder
      descriptionKey="pages.downloadDetail.description"
      titleKey="pages.downloadDetail.title"
    />
  );
}

export const Route = createFileRoute("/downloads/$downloadId")({ component: DownloadDetailPage });
