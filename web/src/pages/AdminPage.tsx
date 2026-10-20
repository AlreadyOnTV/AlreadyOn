import Brand from "../components/Brand";
import ProfileMenu from "./ProfileMenu";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import ChannelsPage from "./admin/ChannelsPage";
import MediaPage from "./admin/MediaPage";
import ManageMediaPage from "./admin/ManageMediaPage";
import ServerPage from "./admin/ServerPage";
import LogsPage from "./admin/LogsPage";
import ApiPage from "./admin/ApiPage";
import AccountsPage from "./admin/AccountsPage";
import DashboardPage from "./admin/DashboardPage";
import { useAccount } from "../users/AccountContext";
import { useUsers } from "../users/UsersContext";
import StartupNotice from "../status/StartupNotice";
import { useStartupStatus } from "../status/useStartupStatus";
import "./AdminPage.css";

export type AdminTab =
  | "dashboard"
  | "accounts"
  | "channels"
  | "library-paths"
  | "manage-media"
  | "server"
  | "logs"
  | "api";

const DEFAULT_TAB: AdminTab = "dashboard";

const TABS: { id: AdminTab; label: string }[] = [
  { id: "dashboard", label: "Dashboard" },
  { id: "accounts", label: "Home Users" },
  { id: "library-paths", label: "Library Paths" },
  { id: "manage-media", label: "Manage Media" },
  { id: "channels", label: "Channels" },
  { id: "server", label: "Server" },
  { id: "logs", label: "Logs" },
  { id: "api", label: "API" },
];

const TAB_COMPONENTS: Record<
  Exclude<AdminTab, "manage-media">,
  () => React.JSX.Element
> = {
  dashboard: DashboardPage,
  accounts: AccountsPage,
  channels: ChannelsPage,
  "library-paths": MediaPage,
  server: ServerPage,
  logs: LogsPage,
  api: ApiPage,
};

function isAdminTab(value: string | undefined): value is AdminTab {
  return value === "manage-media" || (!!value && value in TAB_COMPONENTS);
}

// Server Settings belong to Owner accounts only; the server refuses these
// APIs for Viewer accounts. A Viewer's own account and profiles live on the
// separate Admin Settings page (/account), reached from the profile menu.
export default function AdminPage() {
  const { isOwner: ownerAccount } = useAccount();
  const { isAdmin } = useUsers();
  const isOwner = ownerAccount;
  useEffect(() => {
    document.title = "AlreadyOn - Server Settings";
  }, []);
  const tabs = TABS;
  const defaultTab = DEFAULT_TAB;
  const allowed = (value: string | undefined): value is AdminTab =>
    isAdminTab(value);
  const { tab } = useParams<{ tab: string }>();
  const navigate = useNavigate();
  const [navigationOpen, setNavigationOpen] = useState(false);
  const navigationRef = useRef<HTMLDivElement>(null);
  const { status: startupStatus } = useStartupStatus();
  // Bookmarked/refreshed to an unknown, missing or role-restricted tab: fall
  // back to the default instead of rendering nothing.
  const tabAllowed = allowed(tab);
  useEffect(() => {
    if (!isOwner) {
      // Old bookmarks of the retired My Account tab keep working.
      navigate(tab === "users" && isAdmin ? "/account" : "/", {
        replace: true,
      });
    } else if (tab === "users") {
      navigate("/account", { replace: true });
    } else if (tab === "media") {
      navigate("/admin/library-paths", { replace: true });
    } else if (tab === "commercials") {
      navigate("/admin/library-paths", { replace: true });
    } else if (!tabAllowed) {
      navigate(`/admin/${defaultTab}`, { replace: true });
    }
  }, [isOwner, isAdmin, tab, tabAllowed, defaultTab, navigate]);

  const activeTab: AdminTab = tabAllowed ? (tab as AdminTab) : defaultTab;
  const ActiveTab =
    activeTab === "manage-media" ? null : TAB_COMPONENTS[activeTab];

  useEffect(() => {
    if (!navigationOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (!navigationRef.current?.contains(event.target as Node))
        setNavigationOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setNavigationOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [navigationOpen]);

  const [librarySidebar, setLibrarySidebar] = useState<HTMLDivElement | null>(null);

  const openTab = (next: AdminTab) => {
    setNavigationOpen(false);
    navigate(`/admin/${next}`);
  };

  if (!isOwner) return <></>;
  return (
    <div
      className={
        "admin-page admin-page-sidebar" +
        (activeTab === "logs" ? " admin-page-fit" : "") +
        (activeTab === "manage-media" ? " admin-page-manage-media" : "")
      }
    >
      <div className="admin-topbar">
        <div className="admin-topbar-inner">
          <Brand />
          <div className="admin-nav-back-row">
            <div
              className="admin-mobile-nav"
              ref={navigationRef}
            >
              <button
                type="button"
                className="admin-nav-toggle"
                aria-label="Server Settings Navigation"
                aria-expanded={navigationOpen}
                onClick={() => setNavigationOpen((open) => !open)}
              >
                <span />
                <span />
                <span />
              </button>
              {navigationOpen && (
                <div className="admin-mobile-menu" role="menu">
                  {tabs.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      role="menuitem"
                      aria-current={item.id === activeTab ? "page" : undefined}
                      onClick={() => openTab(item.id)}
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
              )}
            </div>
            <button className="admin-back" onClick={() => navigate("/")}>
              ← Back To Guide
            </button>
          </div>
          <ProfileMenu />
        </div>
      </div>
      <div
        className={
          "admin-layout" +
          (activeTab === "manage-media" ? " admin-layout-manage-media" : "")
        }
      >
        <aside className={activeTab === "manage-media" ? "admin-media-sidebar" : "admin-navigation-sidebar"}>
          <nav className="admin-tabs" aria-label="Server Settings">
            {tabs.map((t) => (
              <button
                key={t.id}
                className={
                  "admin-tab" + (t.id === activeTab ? " admin-tab-active" : "")
                }
                aria-current={t.id === activeTab ? "page" : undefined}
                onClick={() => openTab(t.id)}
              >
                {t.label}
              </button>
            ))}
          </nav>
          {activeTab === "manage-media" && <div id="library-sidebar-filters" ref={setLibrarySidebar} />}
        </aside>
        <div className="admin-content">
          <StartupNotice status={startupStatus} variant="banner" />
          {activeTab === "manage-media" ? (
            <ManageMediaPage filterTarget={librarySidebar} />
          ) : ActiveTab ? (
            <ActiveTab />
          ) : null}
        </div>
      </div>
    </div>
  );
}
