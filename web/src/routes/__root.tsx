import { Activity, Download, Settings2 } from "lucide-react";
import { Link, Outlet, createRootRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

function RootLayout() {
  const { t } = useTranslation();

  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <div className="mx-auto flex min-h-screen max-w-7xl flex-col md:flex-row">
        <aside className="border-b border-slate-800 bg-slate-900/60 md:w-64 md:border-r md:border-b-0">
          <div className="flex items-center gap-3 px-6 py-5">
            <span className="flex size-9 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-400">
              <Activity aria-hidden="true" size={19} />
            </span>
            <div>
              <p className="text-sm font-semibold">{t("app.name")}</p>
              <p className="text-xs text-slate-500">{t("shell.version")}</p>
            </div>
          </div>
          <nav
            aria-label={t("nav.navigation")}
            className="flex gap-1 overflow-x-auto px-3 pb-3 md:flex-col md:px-3 md:pb-6"
          >
            <Link
              activeOptions={{ exact: true }}
              activeProps={{ className: "bg-slate-800 text-slate-100" }}
              className="flex items-center gap-3 rounded-md px-3 py-2 text-sm text-slate-400 transition-colors hover:bg-slate-800/70 hover:text-slate-100"
              to="/"
            >
              <Activity aria-hidden="true" size={17} />
              {t("nav.dashboard")}
            </Link>
            <Link
              activeProps={{ className: "bg-slate-800 text-slate-100" }}
              className="flex items-center gap-3 rounded-md px-3 py-2 text-sm text-slate-400 transition-colors hover:bg-slate-800/70 hover:text-slate-100"
              to="/downloads"
            >
              <Download aria-hidden="true" size={17} />
              {t("nav.downloads")}
            </Link>
            <Link
              activeProps={{ className: "bg-slate-800 text-slate-100" }}
              className="flex items-center gap-3 rounded-md px-3 py-2 text-sm text-slate-400 transition-colors hover:bg-slate-800/70 hover:text-slate-100"
              to="/settings"
            >
              <Settings2 aria-hidden="true" size={17} />
              {t("nav.settings")}
            </Link>
          </nav>
        </aside>
        <main aria-label={t("shell.mainRegion")} className="flex-1 px-6 py-10 md:px-10">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export const Route = createRootRoute({ component: RootLayout });
