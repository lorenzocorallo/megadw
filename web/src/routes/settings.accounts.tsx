import { createFileRoute } from "@tanstack/react-router";
import { PagePlaceholder } from "@/components/page-placeholder";

function AccountsPage() {
  return (
    <PagePlaceholder descriptionKey="pages.accounts.description" titleKey="pages.accounts.title" />
  );
}

export const Route = createFileRoute("/settings/accounts")({ component: AccountsPage });
