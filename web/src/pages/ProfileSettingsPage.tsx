import Brand from '../components/Brand';
import ProfileMenu from './ProfileMenu';
// Self-service settings for the *current* profile (name, PIN, icon, guide
// display), saved to the server. Admin-only actions on OTHER users (like
// resetting a forgotten PIN) live in Admin > User instead — see
// admin/UsersPage.tsx.
import { useEffect, useRef, useState, type ChangeEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useUsers } from '../users/UsersContext';
import { PasswordInput } from '../components/PasswordInput';
import type { ProfilePatch } from '../api/client';
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
  // A preset id, or null. A newly chosen custom image is held in iconBlob
  // (with a local preview URL) until Save uploads it.
  const [draftIcon, setDraftIcon] = useState<string | null>(currentUser.icon);
  const [iconBlob, setIconBlob] = useState<Blob | null>(null);
  const [iconPreview, setIconPreview] = useState<string | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const [pinMode, setPinMode] = useState<'idle' | 'editing'>('idle');
  const [pinValue, setPinValue] = useState('');
  const [pinConfirm, setPinConfirm] = useState('');
  const [pinError, setPinError] = useState<string | null>(null);
  const [confirmRemovePin, setConfirmRemovePin] = useState(false);

  // Every field on this page (name, icon, guide display) is drafted and
  // committed together by Save, so the button's enabled state means the same
  // thing for all of them. PIN changes are separate and apply immediately.
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

  const hasUnsavedChanges =
    draftName.trim() !== currentUser.name ||
    iconBlob !== null ||
    draftIcon !== currentUser.icon ||
    draftTimeFormat !== timeFormat ||
    draftSubtitleMode !== subtitleMode ||
    draftSubtitleLanguage !== subtitleLanguage ||
    draftAudioLanguage !== audioLanguage;

  const handleSave = async () => {
    const name = draftName.trim();
    if (!name) return;
    // Only the changed fields are sent, so another device's edits to other
    // fields aren't overwritten.
    const patch: ProfilePatch = {};
    if (name !== currentUser.name) patch.name = name;
    if (iconBlob === null && draftIcon !== currentUser.icon) patch.icon = draftIcon;
    if (draftTimeFormat !== timeFormat) patch.guideTimeFormat = draftTimeFormat;
    if (draftSubtitleMode !== subtitleMode) patch.subtitleMode = draftSubtitleMode;
    if (draftAudioLanguage !== audioLanguage) patch.audioLanguage = draftAudioLanguage;
    if (draftSubtitleLanguage !== subtitleLanguage) patch.subtitleLanguage = draftSubtitleLanguage;

    setSaving(true);
    setSaveError(null);
    try {
      if (Object.keys(patch).length > 0) await updateUser(currentUser.id, patch);
      if (iconBlob) await uploadIcon(currentUser.id, iconBlob);
      navigate('/');
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : "Couldn't save changes.");
      setSaving(false);
    }
  };

  const startPinEdit = () => {
    setPinValue('');
    setPinConfirm('');
    setPinError(null);
    setPinMode('editing');
    setConfirmRemovePin(false);
  };

  const cancelPinEdit = () => {
    setPinMode('idle');
    setPinError(null);
  };

  const savePin = async () => {
    if (!/^\d{4,6}$/.test(pinValue)) {
      setPinError('PIN must be 4-6 digits.');
      return;
    }
    if (pinValue !== pinConfirm) {
      setPinError("PINs don't match.");
      return;
    }
    try {
      await setPin(currentUser.id, pinValue);
      setPinMode('idle');
      setPinError(null);
    } catch (err) {
      setPinError(err instanceof Error ? err.message : "Couldn't save the PIN.");
    }
  };

  const handleRemovePin = async () => {
    if (!confirmRemovePin) {
      setConfirmRemovePin(true);
      return;
    }
    setConfirmRemovePin(false);
    try {
      await clearPin(currentUser.id);
    } catch (err) {
      setPinError(err instanceof Error ? err.message : "Couldn't remove the PIN.");
    }
  };

  const chooseIcon = (icon: string | null) => {
    setDraftIcon(icon);
    setIconBlob(null);
    setIconPreview(null);
  };

  const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ''; // allow re-selecting the same file again later
    if (!file) return;
    if (!file.type.startsWith('image/')) {
      setUploadError('Please choose an image file.');
      return;
    }
    try {
      const blob = await resizeImageFile(file);
      setIconBlob(blob);
      setIconPreview(URL.createObjectURL(blob));
      setUploadError(null);
    } catch {
      setUploadError("Couldn't read that image.");
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
              <label>Name</label>
              <input type="text" value={draftName} onChange={(e) => setDraftName(e.target.value)} />
            </div>
          </div>

          <div className="admin-card">
            <h3>Icon</h3>
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
          </div>

          <div className="admin-card">
            <h3>PIN</h3>
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
                {pinError && <p className="profile-field-error">{pinError}</p>}
              </>
            ) : (
              <>
                <div className="admin-field-row">
                  <label>New PIN (4-6 Digits)</label>
                  <PasswordInput aria-label="New PIN" autoComplete="new-password" inputMode="numeric" maxLength={6} value={pinValue} onChange={(e) => setPinValue(e.target.value.replace(/\D/g, ''))} />
                </div>
                <div className="admin-field-row">
                  <label>Confirm PIN</label>
                  <PasswordInput
                    aria-label="Confirm PIN"
                    autoComplete="new-password"
                    inputMode="numeric"
                    maxLength={6}
                    value={pinConfirm}
                    onChange={(e) => setPinConfirm(e.target.value.replace(/\D/g, ''))}
                  />
                </div>
                {pinError && <p className="profile-field-error">{pinError}</p>}
                <div className="admin-table-actions">
                  <button className="admin-btn" onClick={() => void savePin()}>
                    Save PIN
                  </button>
                  <button className="admin-btn admin-btn-secondary" onClick={cancelPinEdit}>
                    Cancel
                  </button>
                </div>
              </>
            )}
          </div>

          <div className="admin-card">
            <h3>Guide Display</h3>
            <div className="admin-field-row">
              <label>Time Format</label>
              <select value={draftTimeFormat} onChange={(e) => setDraftTimeFormat(e.target.value as TimeFormat)}>
                <option value="12h-padded">01:00am-02:00am</option>
                <option value="12h">1:00am-2:00am</option>
                <option value="24h">22:00-23:00</option>
              </select>
            </div>
          </div>

          <div className="admin-card">
            <h3>Audio</h3>
            <div className="admin-field-row">
              <label htmlFor="preferred-audio-language">Preferred Audio Language</label>
              <select id="preferred-audio-language" value={draftAudioLanguage} onChange={(e) => setDraftAudioLanguage(e.target.value)}>
                {SUBTITLE_LANGUAGES.map((code) => (
                  <option key={code} value={code}>
                    {languageLabel(code)}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div className="admin-card">
            <h3>Subtitles</h3>
            <div className="admin-field-row">
              <label htmlFor="subtitle-language">Subtitle language</label>
              <select id="subtitle-language" value={draftSubtitleLanguage} onChange={(e) => setDraftSubtitleLanguage(e.target.value)}>
                {SUBTITLE_LANGUAGES.map((code) => <option key={code} value={code}>{languageLabel(code)}</option>)}
              </select>
            </div>
            <div className="admin-field-row">
              <label htmlFor="subtitle-mode">Subtitles</label>
              <select id="subtitle-mode" value={draftSubtitleMode} onChange={(e) => setDraftSubtitleMode(e.target.value as typeof subtitleMode)}>
                <option value="always">Always on</option>
                <option value="other_audio">For other audio</option>
                <option value="off">Off</option>
              </select>
            </div>
          </div>

          {saveError && <p className="profile-field-error">{saveError}</p>}
          <div className="admin-table-actions">
            <button className="admin-btn" disabled={!hasUnsavedChanges || saving} onClick={() => void handleSave()}>
              Save Changes
            </button>
            <button className="admin-btn admin-btn-secondary" onClick={() => navigate('/')}>
              Cancel
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
