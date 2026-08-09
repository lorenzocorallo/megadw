import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function ProxiesPage() {
  return (
    <PagePlaceholder descriptionKey="pages.proxies.description" titleKey="pages.proxies.title" />
  );
}

export const Route = createFileRoute("/settings/proxies")({ component: ProxiesPage });
