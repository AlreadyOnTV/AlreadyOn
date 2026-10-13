import Brand from '../components/Brand';
import ProfileMenu from './ProfileMenu';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import ChannelsPage from './admin/ChannelsPage';
import CommercialsPage from './admin/CommercialsPage';
import MediaPage from './admin/MediaPage';
import ServerPage from './admin/ServerPage';
import LogsPage from './admin/LogsPage';
import ApiPage from './admin/ApiPage';
import AccountsPage from './admin/AccountsPage';
import { useAccount } from '../users/AccountContext';
import { useUsers } from '../users/UsersContext';
import StartupNotice from '../status/StartupNotice';
import { useStartupStatus } from '../status/useStartupStatus';
import './AdminPage.css';

type AdminTab = 'accounts' | 'channels' | 'commercials' | 'media' | 'server' | 'logs' | 'api';

const DEFAULT_TAB: AdminTab = 'accounts';

const TABS: { id: AdminTab; label: string }[] = [
  { id: 'accounts', label: 'Accounts' },
  { id: 'media', label: 'Media' },
  { id: 'commercials', label: 'Commercials' },
  { id: 'channels', label: 'Channels' },
  { id: 'server', label: 'Server' },
  { id: 'logs', label: 'Logs' },
  { id: 'api', label: 'API' },
];

const TAB_COMPONENTS: Record<AdminTab, () => React.JSX.Element> = {
  accounts: AccountsPage,
  channels: ChannelsPage,
  commercials: CommercialsPage,
  media: MediaPage,
  server: ServerPage,
  logs: LogsPage,
  api: ApiPage,
};

function isAdminTab(value: string | undefined): value is AdminTab {
  return !!value && value in TAB_COMPONENTS;
}

// Server Settings belong to Owner accounts only; the server refuses these
// APIs for Viewer accounts. A Viewer's own account and profiles live on the
// separate Admin Settings page (/account), reached from the profile menu.
export default function AdminPage() {
  const { isOwner: ownerAccount } = useAccount();
  const { isAdmin } = useUsers();
  const isOwner = ownerAccount;
  useEffect(() => {
    document.title = 'AlreadyOn - Server Settings';
  }, []);
  const tabs = TABS;
  const defaultTab = DEFAULT_TAB;
  const allowed = (value: string | undefined): value is AdminTab => isAdminTab(value);
  const { tab } = useParams<{ tab: string }>();
  const navigate = useNavigate();
  const [menuOpen, setMenuOpen] = useState(false);
  const mobileNav = useRef<HTMLDivElement>(null);
  const { status: startupStatus } = useStartupStatus();
  // Bookmarked/refreshed to an unknown, missing or role-restricted tab: fall
  // back to the default instead of rendering nothing.
  const tabAllowed = allowed(tab);
  useEffect(() => {
    if (!isOwner) {
      // Old bookmarks of the retired My Account tab keep working.
      navigate(tab === 'users' && isAdmin ? '/account' : '/', { replace: true });
    } else if (tab === 'users') {
      navigate('/account', { replace: true });
    } else if (!tabAllowed) {
      navigate(`/admin/${defaultTab}`, { replace: true });
    }
  }, [isOwner, isAdmin, tab, tabAllowed, defaultTab, navigate]);

  const activeTab: AdminTab = tabAllowed ? (tab as AdminTab) : defaultTab;
  const ActiveTab = TAB_COMPONENTS[activeTab];

  // The compact navigation closes on outside clicks and Escape, matching the
  // guide's menus while leaving the desktop sidebar permanently visible.
  useEffect(() => {
    if (!menuOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (!mobileNav.current?.contains(event.target as Node)) setMenuOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMenuOpen(false);
    };
    document.addEventListener('pointerdown', closeOutside);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOutside);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [menuOpen]);

  const openTab = (next: AdminTab) => {
    setMenuOpen(false);
    navigate(`/admin/${next}`);
  };

  // Extension-specific hints also cover dynamically mounted controls on every
  // settings tab. The login surface is outside this scope and retains autofill.
  useEffect(() => {
    const root = document.querySelector('.admin-page');
    if (!root) return;
    const suppress = () => root.querySelectorAll('input:not([type="checkbox"]):not([type="radio"]), textarea, form').forEach(field => {
      field.setAttribute('autocomplete', 'off');
      field.setAttribute('data-bwignore', 'true');
      field.setAttribute('data-1p-ignore', 'true');
      field.setAttribute('data-lpignore', 'true');
      field.setAttribute('data-form-type', 'other');
    });
    suppress();
    const observer = new MutationObserver(suppress);
    observer.observe(root, {childList:true, subtree:true});
    return () => observer.disconnect();
  }, []);
  if (!isOwner) return <></>;
  return (
    <div className={'admin-page admin-page-sidebar' + (activeTab === 'logs' ? ' admin-page-fit' : '')}>
      <div className="admin-topbar">
        <div className="admin-topbar-inner">
          <Brand />
          <div className="admin-mobile-nav" ref={mobileNav}>
            <button
              type="button"
              className="admin-nav-toggle"
              aria-label="Server Settings Navigation"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen((open) => !open)}
            >
              <span />
              <span />
              <span />
            </button>
            {menuOpen && (
              <div className="admin-mobile-menu" role="menu">
                {tabs.map((t) => (
                  <button key={t.id} type="button" role="menuitem" aria-current={t.id === activeTab ? 'page' : undefined} onClick={() => openTab(t.id)}>
                    {t.label}
                  </button>
                ))}
              </div>
            )}
          </div>
          <button className="admin-back" onClick={() => navigate('/')}>
            ← Back To Guide
          </button>
          <ProfileMenu />
        </div>
      </div>
      <div className="admin-layout">
        <nav className="admin-tabs" aria-label="Server Settings">
          {tabs.map((t) => (
            <button
              key={t.id}
              className={'admin-tab' + (t.id === activeTab ? ' admin-tab-active' : '')}
              aria-current={t.id === activeTab ? 'page' : undefined}
              onClick={() => openTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </nav>
        <div className="admin-content">
          <StartupNotice status={startupStatus} variant="banner" />
          <ActiveTab />
        </div>
      </div>
    </div>
  );
}
