// Reuse the existing monitoring content outside owner-only Server Settings.
import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import Brand from '../components/Brand';
import ProfileMenu from './ProfileMenu';
import DashboardPage from './admin/DashboardPage';
import StartupNotice from '../status/StartupNotice';
import { useStartupStatus } from '../status/useStartupStatus';
import './AdminPage.css';

export default function DashboardView() {
  const navigate = useNavigate();
  const { status } = useStartupStatus();
  useEffect(() => { document.title = 'AlreadyOn - Dashboard'; }, []);
  return <div className="admin-page">
    <div className="admin-topbar"><div className="admin-topbar-inner">
      <Brand />
      <button className="admin-back" onClick={() => navigate('/')}>
        ← Back To Guide
      </button>
      <ProfileMenu />
    </div></div>
    <div className="admin-content">
      <StartupNotice status={status} variant="banner" />
      <DashboardPage />
    </div>
  </div>;
}
