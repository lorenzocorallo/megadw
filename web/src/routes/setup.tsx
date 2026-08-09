import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function SetupPage() {
  return <PagePlaceholder descriptionKey="pages.setup.description" titleKey="pages.setup.title" />;
}

export const Route = createFileRoute("/setup")({ component: SetupPage });
