// Render a preset, uploaded image, or initial using the shared avatar catalog.
import { findPresetIcon, isCustomIcon } from './avatarPresets';

interface UserAvatarProps {
  name: string;
  icon?: string | null;
  size?: number;
}

export function UserAvatar({ name, icon, size = 32 }: UserAvatarProps) {
  const style = {
    width: size,
    height: size,
    fontSize: Math.round(size * 0.55),
    borderRadius: '50%',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    overflow: 'hidden',
  } as const;

  if (isCustomIcon(icon)) {
    return (
      <span style={style}>
        <img src={icon ?? undefined} alt="" width={size} height={size} style={{ objectFit: 'cover' }} />
      </span>
    );
  }

  const preset = findPresetIcon(icon);
  if (preset) {
    return (
      <span style={{ ...style, background: preset.bg }} title={preset.label}>
        {preset.emoji}
      </span>
    );
  }

  // Fallback: first initial on a neutral background.
  return (
    <span style={{ ...style, background: 'var(--surface-alt)', color: 'var(--text-muted)', fontWeight: 'bold' }}>
      {name.trim().charAt(0).toUpperCase() || '?'}
    </span>
  );
}
