// Monitoring is available to every signed-in profile; settings retain owner checks.
import { Navigate, Route, Routes } from "react-router-dom";
import GuidePage from "./pages/GuidePage";
import AdminPage from "./pages/AdminPage";
import DashboardView from "./pages/DashboardView";
import ProfileSettingsPage from "./pages/ProfileSettingsPage";
import AdminSettingsPage from "./pages/AdminSettingsPage";
import { useUsers } from "./users/UsersContext";

function App() {
  const { currentUserId } = useUsers();
  return (
    <Routes>
      <Route path="/" element={<GuidePage key={currentUserId} />} />
      <Route path="/profile" element={<ProfileSettingsPage />} />
      <Route path="/account" element={<AdminSettingsPage />} />
      <Route path="/dashboard" element={<DashboardView />} />
      <Route path="/admin/dashboard" element={<Navigate to="/dashboard" replace />} />
      <Route path="/admin" element={<Navigate to="/admin/accounts" replace />} />
      <Route path="/admin/:tab" element={<AdminPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

export default App;
