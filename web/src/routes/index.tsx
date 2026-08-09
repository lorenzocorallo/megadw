import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function DashboardPage() {
  return (
    <PagePlaceholder
      descriptionKey="pages.dashboard.description"
      titleKey="pages.dashboard.title"
    />
  );
}

export const Route = createFileRoute("/")({ component: DashboardPage });
