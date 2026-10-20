import Brand from '../components/Brand';
import ProfileMenu from './ProfileMenu';
// Admin Settings: the signed-in account (password, sign-in details) and that
// account's profiles. It replaced the My Account tab of Server Settings so
// Viewer accounts, which have no Server Settings, reach it the same way as
// Owners: from the guide's profile menu while an admin profile is active.
import { useEffect } from 'react';
import { Navigate, useNavigate } from 'react-router-dom';
import UsersPage from './admin/UsersPage';
import { useUsers } from '../users/UsersContext';
import './AdminPage.css';

export default function AdminSettingsPage() {
  const { isAdmin } = useUsers();
  const navigate = useNavigate();
  useEffect(() => {
    document.title = 'AlreadyOn - Admin Settings';
  }, []);
  if (!isAdmin) return <Navigate to="/" replace />;
  return (
    <div className="admin-page">
      <div className="admin-topbar">
        <div className="admin-topbar-inner">
          <Brand />
          <div className="admin-nav-back-row">
            <button className="admin-back" onClick={() => navigate('/')}>
              ← Back To Guide
            </button>
          </div>
          <ProfileMenu />
        </div>
      </div>
      <div className="admin-content">
        <UsersPage />
      </div>
    </div>
  );
}
