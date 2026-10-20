// Live monitoring is grouped in viewing priority; polling ends when leaving this tab.
import ServerActivity from "./ServerActivity";
import ServerMetrics from "./ServerMetrics";
import "./AdminShared.css";

export default function DashboardPage() {
  return (
    <div className="admin-section">
      <ServerActivity />
      <div className="admin-card">
        <ServerMetrics title="Server Metrics" />
      </div>
    </div>
  );
}
