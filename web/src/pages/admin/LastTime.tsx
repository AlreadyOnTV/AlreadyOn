// The small note beside a refresh button; nothing until the time is known.
export function LastTime({ label, at }: { label: string; at: string | null | undefined }) {
  if (at === undefined) return null;
  return (
    <span className="admin-section-hint" style={{ margin: 0 }}>
      {label}: {at === null ? 'Never' : new Date(at).toLocaleString()}
    </span>
  );
}
