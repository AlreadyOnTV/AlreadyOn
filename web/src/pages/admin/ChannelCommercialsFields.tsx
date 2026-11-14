// Commercial toggles edit a draft; the parent saves it with the channel.
import type { ChannelCommercialsDraft } from './channelCommercials';
import SettingToggle from './SettingToggle';

export default function ChannelCommercialsFields({ draft, onChange }: { draft: ChannelCommercialsDraft; onChange: (d: ChannelCommercialsDraft) => void }) {
  return (
    <>
      <div className="admin-field-row">
        <label>Enable Commercials</label>
        <div className="included-paths">
          <label>
            <SettingToggle label="Enable Commercials" checked={draft.enabled} onChange={(enabled) => onChange({ ...draft, enabled })} />
          </label>
        </div>
      </div>
      {draft.enabled && (
        <div className="admin-field-row">
          <label>Commercial Paths</label>
          <div className="included-paths">
            {draft.available.map((path) => (
              <label key={path}>
                <SettingToggle
                  label={path}
                  checked={draft.selected.includes(path)}
                  onChange={(on) => onChange({ ...draft, selected: on ? [...draft.selected, path] : draft.selected.filter((p) => p !== path) })}
                />
                <span>{path}</span>
              </label>
            ))}
            {draft.available.length === 0 && <span className="admin-section-hint">No commercial folders are configured yet.</span>}
          </div>
        </div>
      )}
    </>
  );
}
