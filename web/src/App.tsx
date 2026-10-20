// Server navigation, including Dashboard, remains owner-only.
import { Navigate, Route, Routes } from "react-router-dom";
import GuidePage from "./pages/GuidePage";
import AdminPage from "./pages/AdminPage";
import ProfileSettingsPage from "./pages/ProfileSettingsPage";
import AdminSettingsPage from "./pages/AdminSettingsPage";
import { useUsers } from "./users/UsersContext";

function App() {
  const { currentUserId } = useUsers();
  return (
    <Routes>
      <Route path="/" element={<GuidePage key={currentUserId} />} />
      <Route path="/profile" element={<ProfileSettingsPage key={currentUserId} />} />
      <Route path="/account" element={<AdminSettingsPage />} />
      <Route path="/dashboard" element={<Navigate to="/admin/dashboard" replace />} />
      <Route path="/admin" element={<Navigate to="/admin/dashboard" replace />} />
      <Route path="/admin/:tab" element={<AdminPage />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

export default App;
