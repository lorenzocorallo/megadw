import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function AppearancePage() {
  return (
    <PagePlaceholder
      descriptionKey="pages.appearance.description"
      titleKey="pages.appearance.title"
    />
  );
}

export const Route = createFileRoute("/settings/appearance")({ component: AppearancePage });
