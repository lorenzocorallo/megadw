import { useEffect } from "react";
import { Activity, Download, LogOut, Settings2 } from "lucide-react";
import { Link, Outlet, createRootRoute, useLocation, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { getAuthStatus, logout } from "@/api/client";

function RootLayout() {
  const { t } = useTranslation();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const authStatus = useQuery({ queryKey: ["auth-status"], queryFn: getAuthStatus, retry: false });
  const logoutMutation = useMutation({
    mutationFn: logout,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["auth-status"] });
      void navigate({ to: "/login" });
    },
  });

  useEffect(() => {
    if (!authStatus.data) return;
    if (authStatus.data.setupRequired && location.pathname !== "/setup") {
      void navigate({ to: "/setup", replace: true });
    } else if (
      !authStatus.data.setupRequired &&
      !authStatus.data.authenticated &&
      location.pathname !== "/login"
    ) {
      void navigate({ to: "/login", replace: true });
    }
  }, [authStatus.data, location.pathname, navigate]);

  const publicRoute = location.pathname === "/setup" || location.pathname === "/login";

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
            {!publicRoute ? (
              <>
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
              </>
            ) : null}
          </nav>
          {!publicRoute && authStatus.data?.authenticated ? (
            <button
              className="mx-3 mb-5 flex items-center gap-3 rounded-md px-3 py-2 text-sm text-slate-400 transition-colors hover:bg-slate-800/70 hover:text-slate-100"
              type="button"
              onClick={() => logoutMutation.mutate()}
            >
              <LogOut aria-hidden="true" size={17} />
              {t("auth.logout")}
            </button>
          ) : null}
        </aside>
        <main aria-label={t("shell.mainRegion")} className="flex-1 px-6 py-10 md:px-10">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export const Route = createRootRoute({ component: RootLayout });
