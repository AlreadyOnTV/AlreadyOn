import Brand from '../components/Brand';
import ProfileMenu from './ProfileMenu';
// Self-service settings for the *current* profile (name, PIN, icon, guide
// display), saved to the server. Admin-only actions on OTHER users (like
// resetting a forgotten PIN) live in Admin > User instead — see
// admin/UsersPage.tsx.
import { useCallback, useEffect, useRef, useState, type ChangeEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useUsers } from '../users/UsersContext';
import { PasswordInput } from '../components/PasswordInput';
import { useProfileAutosave, type ProfileSetting } from './useProfileAutosave';
import { PRESET_ICONS, UserAvatar } from '../users/avatars';
import { DEFAULT_SUBTITLE_LANGUAGE, SUBTITLE_LANGUAGES, languageLabel } from '../playback/subtitles';
import { useGuideSettings, type TimeFormat } from '../settings/GuideSettingsContext';
import './AdminPage.css';
import './admin/AdminShared.css';
import './ProfileSettingsPage.css';

const MAX_ICON_DIMENSION = 128;

// Crops to a centered square and scales to a small PNG, so uploads stay tiny.
function resizeImageFile(file: File): Promise<Blob> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('Could not load image'));
    };
    img.onload = () => {
      URL.revokeObjectURL(url);
      const canvas = document.createElement('canvas');
      canvas.width = MAX_ICON_DIMENSION;
      canvas.height = MAX_ICON_DIMENSION;
      const ctx = canvas.getContext('2d');
      if (!ctx) {
        reject(new Error('Canvas not supported'));
        return;
      }
      const side = Math.min(img.width, img.height);
      const sx = (img.width - side) / 2;
      const sy = (img.height - side) / 2;
      ctx.drawImage(img, sx, sy, side, side, 0, 0, MAX_ICON_DIMENSION, MAX_ICON_DIMENSION);
      canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('Could not encode image'))), 'image/png');
    };
    img.src = url;
  });
}

export default function ProfileSettingsPage() {
  useEffect(() => {
    document.title = 'AlreadyOn - Profile Settings';
  }, []);
  const { currentUser, updateUser, setPin, clearPin, uploadIcon } = useUsers();
  const { timeFormat } = useGuideSettings();
  const navigate = useNavigate();
  const [draftName, setDraftName] = useState(currentUser.name);
  // A custom icon previews locally while its upload completes.
  const [draftIcon, setDraftIcon] = useState<string | null>(currentUser.icon);
  const [iconPreview, setIconPreview] = useState<string | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [pinMode, setPinMode] = useState<'idle' | 'editing'>('idle');
  const [pinValue, setPinValue] = useState('');
  const [pinConfirm, setPinConfirm] = useState('');
  const [pinError, setPinError] = useState<string | null>(null);
  const [pinSaving, setPinSaving] = useState(false);
  const [confirmRemovePin, setConfirmRemovePin] = useState(false);

  // Keep input drafts independent from optimistic profile updates and refreshes.
  const [draftTimeFormat, setDraftTimeFormat] = useState(timeFormat);
  const subtitleMode = currentUser.subtitleMode ?? (currentUser.subtitlesEnabled ? 'always' : 'off');
  const [draftSubtitleMode, setDraftSubtitleMode] = useState(subtitleMode);
  const audioLanguage = currentUser.audioLanguage ?? 'en';
  const [draftAudioLanguage, setDraftAudioLanguage] = useState(audioLanguage);
  const subtitleLanguage = currentUser.subtitleLanguage ?? DEFAULT_SUBTITLE_LANGUAGE;
  const [draftSubtitleLanguage, setDraftSubtitleLanguage] = useState(subtitleLanguage);

  const previousProfile = useRef(currentUser);
  useEffect(() => {
    // Keep local edits while rebasing untouched fields on remote profile refreshes.
    const previous = previousProfile.current;
    previousProfile.current = currentUser;
    setDraftName(value => value === previous.name ? currentUser.name : value);
    setDraftIcon(value => value === previous.icon ? currentUser.icon : value);
    setDraftTimeFormat(value => value === (previous.guideTimeFormat ?? '12h') ? timeFormat : value);
    setDraftSubtitleMode(value => value === (previous.subtitleMode ?? (previous.subtitlesEnabled ? 'always' : 'off')) ? subtitleMode : value);
    setDraftAudioLanguage(value => value === (previous.audioLanguage ?? 'en') ? audioLanguage : value);
    setDraftSubtitleLanguage(value => value === (previous.subtitleLanguage ?? DEFAULT_SUBTITLE_LANGUAGE) ? subtitleLanguage : value);
  }, [currentUser, timeFormat, subtitleMode, audioLanguage, subtitleLanguage]);

  const { save, saved, errors } = useProfileAutosave({
    name: currentUser.name, icon: currentUser.icon, guideTimeFormat: timeFormat,
    audioLanguage, subtitleLanguage, subtitleMode,
  });
  const updateUserRef = useRef(updateUser);
  useEffect(() => { updateUserRef.current = updateUser; });
  const saveName = useCallback((value: string) => {
    const name = value.trim();
    if (name) void save('name', name, () => updateUserRef.current(currentUser.id, { name }));
  }, [save, currentUser.id]);
  useEffect(() => {
    const timer = window.setTimeout(() => saveName(draftName), 600);
    return () => window.clearTimeout(timer);
  }, [draftName, saveName]);
  useEffect(() => {
    if (!iconPreview) return;
    return () => URL.revokeObjectURL(iconPreview);
  }, [iconPreview]);
  const iconChoice = useRef(0);
  const lastUpload = useRef<{ hash: string; icon: string | null } | null>(null);
  const feedback = (key: ProfileSetting) => (
    <span key={saved[key] ?? 'idle'} className={'profile-save-check' + (saved[key] ? ' is-saved' : '')}
      role="status" aria-label={saved[key] ? `${key} saved` : undefined}>{saved[key] ? '✓' : ''}</span>
  );
  const fieldError = (key: ProfileSetting) => errors[key] && <p className="profile-field-error" role="alert">{errors[key]}</p>;

  const startPinEdit = () => {
    setPinValue('');
    setPinConfirm('');
    setPinError(null);
    setPinMode('editing');
    setConfirmRemovePin(false);
  };

  const cancelPinEdit = () => {
    setPinMode('idle');
    setPinValue('');
    setPinConfirm('');
    setPinError(null);
  };

  const savePin = async () => {
    if (pinSaving) return;
    if (!/^\d{4,6}$/.test(pinValue)) {
      setPinError('PIN must be 4-6 digits.');
      return;
    }
    if (pinValue !== pinConfirm) {
      setPinError("PINs don't match.");
      return;
    }
    setPinSaving(true);
    setPinError(null);
    try {
      if (await save('pin', pinValue, () => setPin(currentUser.id, pinValue))) cancelPinEdit();
    } finally {
      setPinSaving(false);
    }
  };

  const handleRemovePin = async () => {
    if (!confirmRemovePin) {
      setConfirmRemovePin(true);
      return;
    }
    setConfirmRemovePin(false);
    await save('pin', null, () => clearPin(currentUser.id));
  };

  const chooseIcon = (icon: string | null) => {
    iconChoice.current++;
    setDraftIcon(icon);
    setIconPreview(null);
    setUploadError(null);
    void save('icon', icon, () => updateUser(currentUser.id, { icon }));
  };

  const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    const choice = ++iconChoice.current;
    if (!file.type.startsWith('image/')) {
      setUploadError('Please choose an image file.');
      return;
    }
    try {
      const blob = await resizeImageFile(file);
      const hash = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', await blob.arrayBuffer())),
        byte => byte.toString(16).padStart(2, '0')).join('');
      if (choice !== iconChoice.current) return;
      if (lastUpload.current?.hash === hash && lastUpload.current.icon === currentUser.icon) return;
      setIconPreview(URL.createObjectURL(blob));
      setUploadError(null);
      await save('icon', `upload:${hash}`, async () => {
        const profile = await uploadIcon(currentUser.id, blob);
        lastUpload.current = { hash, icon: profile.icon };
      });
      if (choice === iconChoice.current) setIconPreview(null);
    } catch {
      if (choice === iconChoice.current) setUploadError("Couldn't read that image.");
    }
  };

  return (
    <div className="admin-page profile-settings-page">
      <div className="admin-topbar">
        <div className="admin-topbar-inner">
          <Brand />
          <ProfileMenu />
        </div>
      </div>
      <div className="admin-content">
        <div className="settings-back-row">
          <button className="admin-back" onClick={() => navigate('/')}>
            ← Back To Guide
          </button>
        </div>
        <div className="admin-section">
          <div className="admin-card">
            <h3>Display Name</h3>
            <div className="admin-field-row">
              <label htmlFor="profile-name">Name</label>
              <div className="profile-setting-control">
                <input id="profile-name" type="text" value={draftName} onChange={(e) => setDraftName(e.target.value)} onBlur={() => saveName(draftName)} />
                {feedback('name')}
              </div>
            </div>
            {!draftName.trim() && <p className="profile-field-error">Name is required.</p>}
            {fieldError('name')}
          </div>

          <div className="admin-card">
            <h3>Icon {feedback('icon')}</h3>
            <div className="profile-icon-preview">
              <UserAvatar name={draftName || currentUser.name} icon={iconPreview ?? draftIcon} size={64} />
              <button type="button" className="admin-btn admin-btn-secondary" onClick={() => fileInputRef.current?.click()}>
                Upload Custom Icon
              </button>
              {(draftIcon || iconPreview) && (
                <button type="button" className="admin-btn admin-btn-secondary" onClick={() => chooseIcon(null)}>
                  Remove Icon
                </button>
              )}
            </div>
            <div className="profile-icon-grid">
              {PRESET_ICONS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className={'profile-icon-option' + (draftIcon === p.id && !iconPreview ? ' profile-icon-option-active' : '')}
                  style={{ background: p.bg }}
                  title={p.label}
                  onClick={() => chooseIcon(p.id)}
                >
                  {p.emoji}
                </button>
              ))}
            </div>
            <input ref={fileInputRef} type="file" accept="image/*" style={{ display: 'none' }} onChange={handleFileChange} />
            {uploadError && <p className="profile-field-error">{uploadError}</p>}
            {fieldError('icon')}
          </div>

          <div className="admin-card">
            <h3>PIN {feedback('pin')}</h3>
            {fieldError('pin')}
            {pinMode === 'idle' ? (
              <>
                <div className="admin-field-row profile-pin-status">
                  <label>Status</label>
                  <div className="profile-pin-controls">
                    <span className={'admin-badge' + (currentUser.hasPin ? ' admin-badge-on' : '')}>{currentUser.hasPin ? 'PIN Is Set' : 'No PIN Set'}</span>
                    <button className="admin-btn admin-btn-secondary" onClick={startPinEdit}>
                      {currentUser.hasPin ? 'Change PIN' : 'Set PIN'}
                    </button>
                    {currentUser.hasPin && (
                      <button
                        className={'admin-btn admin-btn-secondary' + (confirmRemovePin ? ' admin-btn-danger' : '')}
                        onClick={() => void handleRemovePin()}
                      >
                        {confirmRemovePin ? 'Confirm remove?' : 'Remove PIN'}
                      </button>
                    )}
                  </div>
                </div>
              </>
            ) : (
              <>
                <div className="admin-field-row">
                  <label>New PIN (4-6 Digits)</label>
                  <PasswordInput aria-label="New PIN" autoComplete="new-password" inputMode="numeric" disabled={pinSaving} maxLength={6} value={pinValue} onChange={(e) => setPinValue(e.target.value.replace(/\D/g, ''))} />
                </div>
                <div className="admin-field-row">
                  <label>Confirm PIN</label>
                  <PasswordInput
                    aria-label="Confirm PIN"
                    autoComplete="new-password"
                    inputMode="numeric"
                    maxLength={6}
                    disabled={pinSaving}
                    value={pinConfirm}
                    onChange={(e) => setPinConfirm(e.target.value.replace(/\D/g, ''))}
                  />
                </div>
                {pinError && <p className="profile-field-error" role="alert">{pinError}</p>}
                <div className="admin-table-actions">
                  <button className="admin-btn" disabled={pinSaving} onClick={() => void savePin()}>Save PIN</button>
                  <button className="admin-btn admin-btn-secondary" disabled={pinSaving} onClick={cancelPinEdit}>Cancel</button>
                </div>
              </>
            )}
          </div>

          <div className="admin-card">
            <h3>Guide Display</h3>
            <div className="admin-field-row">
              <label htmlFor="profile-time-format">Time Format</label>
              <div className="profile-setting-control">
                <select id="profile-time-format" value={draftTimeFormat} onChange={(e) => {
                  const value = e.target.value as TimeFormat;
                  setDraftTimeFormat(value);
                  void save('guideTimeFormat', value, () => updateUser(currentUser.id, { guideTimeFormat: value }));
                }}>
                  <option value="12h-padded">01:00am-02:00am</option>
                  <option value="12h">1:00am-2:00am</option>
                  <option value="24h">22:00-23:00</option>
                </select>
                {feedback('guideTimeFormat')}
              </div>
            </div>
            {fieldError('guideTimeFormat')}
          </div>

          <div className="admin-card">
            <h3>Audio</h3>
            <div className="admin-field-row">
              <label htmlFor="preferred-audio-language">Preferred Audio Language</label>
              <div className="profile-setting-control">
                <select id="preferred-audio-language" value={draftAudioLanguage} onChange={(e) => {
                  const value = e.target.value;
                  setDraftAudioLanguage(value);
                  void save('audioLanguage', value, () => updateUser(currentUser.id, { audioLanguage: value }));
                }}>
                  {SUBTITLE_LANGUAGES.map((code) => (
                    <option key={code} value={code}>
                      {languageLabel(code)}
                    </option>
                  ))}
                </select>
                {feedback('audioLanguage')}
              </div>
            </div>
            {fieldError('audioLanguage')}
          </div>
          <div className="admin-card">
            <h3>Subtitles</h3>
            <div className="admin-field-row">
              <label htmlFor="subtitle-language">Subtitle language</label>
              <div className="profile-setting-control">
                <select id="subtitle-language" value={draftSubtitleLanguage} onChange={(e) => {
                  const value = e.target.value;
                  setDraftSubtitleLanguage(value);
                  void save('subtitleLanguage', value, () => updateUser(currentUser.id, { subtitleLanguage: value }));
                }}>
                  {SUBTITLE_LANGUAGES.map((code) => <option key={code} value={code}>{languageLabel(code)}</option>)}
                </select>
                {feedback('subtitleLanguage')}
              </div>
            </div>
            {fieldError('subtitleLanguage')}
            <div className="admin-field-row">
              <label htmlFor="subtitle-mode">Subtitles</label>
              <div className="profile-setting-control">
                <select id="subtitle-mode" value={draftSubtitleMode} onChange={(e) => {
                  const value = e.target.value as typeof subtitleMode;
                  setDraftSubtitleMode(value);
                  void save('subtitleMode', value, () => updateUser(currentUser.id, { subtitleMode: value }));
                }}>
                  <option value="always">Always on</option>
                  <option value="other_audio">For other audio</option>
                  <option value="off">Off</option>
                </select>
                {feedback('subtitleMode')}
              </div>
            </div>
            {fieldError('subtitleMode')}
          </div>
        </div>
      </div>
    </div>
  );
}
