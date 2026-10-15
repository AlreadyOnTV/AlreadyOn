// Custom player controls. They replace the browser's own bar so the total time
// can be the episode's real length and the menu can carry subtitles. Buttons
// take keyboard focus only for expanded playback; preview arrows browse the guide.
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { ProgramOverlay } from './ProgramOverlay';
import type { PlaybackSession, AudioTrack, SubtitleTrack } from './types';
import { canonicalLanguage, languageLabel, trackKey } from './subtitles';


function formatClock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) seconds = 0;
  const s = Math.floor(seconds);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

export interface ControlsProps {
  onWakeControls: () => void;
  onHideControls: () => void;
  session?: PlaybackSession;
  paused: boolean;
  time: number;
  duration: number;
  volume: number;
  muted: boolean;
  // The viewer's quality ceiling (0 = automatic) and the heights the source can offer.
  quality: number;
  qualities: number[];
  tracks: SubtitleTrack[];
  audioTracks: AudioTrack[];
  audioTrackIndex?: number;
  onAudioTrack: (track: AudioTrack) => void;
  // The chosen subtitle language, or null when subtitles are off.
  subtitleLanguage: string | null;
  onTogglePlay: () => void;
  onVolume: (v: number) => void;
  onToggleMute: () => void;
  onQuality: (height: number) => void;
  onSubtitleLanguage: (language: string | null) => void;
  onPictureInPicture: () => void;
  detailsOn: boolean;
  onToggleDetails: () => void;
  onFullscreen: () => void;
  // Only the guide's preview can grow into a large player.
  large?: boolean;
  onToggleLarge?: () => void;
}

const noFocus = { tabIndex: -1, onMouseDown: (e: React.MouseEvent) => e.preventDefault() } as const;

function Icon({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
      {children}
    </svg>
  );
}

type Menu = null | 'captions' | 'settings' | 'quality' | 'audio';

const playerLanguage = (code?: string) => {
  const language = (code ?? '').trim().toLowerCase().replaceAll('_', '-').split('-')[0];
  return /^[a-z]{2,3}$/.test(language) && !['und', 'zxx', 'mul'].includes(language) ? language : 'unknown';
};

const qualityLabel = (height: number) => (height === 0 ? 'Auto' : height === 2160 ? '4K' : `${height}p`);

export function PlayerControls(p: ControlsProps) {
  const [menu, setMenu] = useState<Menu>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const settingsButtonRef = useRef<HTMLButtonElement>(null);
  const { onWakeControls, onHideControls, large } = p;
  // Capture expanded-player navigation before the Guide/global shortcuts.
  // Menu arrows own their cursor; only Up/Down outside menus reach channel tuning.
  useEffect(() => {
    const expanded = () => !!rootRef.current?.closest('.playback-player.is-large') || !!document.fullscreenElement?.contains(rootRef.current);
    const keydown = (event: KeyboardEvent) => {
      if (!expanded() || event.ctrlKey || event.altKey || event.metaKey || event.defaultPrevented) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest('input:not([type="range"]), textarea, select, dialog, .profile-menu, .page-header')) return;
      const root = rootRef.current;
      if (!root) return;
      // Read the rendered menu, so a rapid next key cannot use a stale effect closure.
      const activeMenu = (root.dataset.playerMenu ?? null) as Menu;
      const arrows = ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'];
      if (!activeMenu && (event.key === 'ArrowUp' || event.key === 'ArrowDown')) return;
      if (![...arrows, 'Enter', ' ', 'Escape'].includes(event.key)) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.key === 'Escape') {
        if (activeMenu) {
          setMenu(activeMenu === 'settings' ? null : 'settings');
          onWakeControls();
          if (activeMenu === 'settings') settingsButtonRef.current?.focus();
        } else {
          onHideControls();
          if (document.activeElement instanceof HTMLElement && root.contains(document.activeElement)) document.activeElement.blur();
        }
        return;
      }
      onWakeControls();
      const buttons = [...root.querySelectorAll<HTMLButtonElement>(activeMenu ? '[role="menu"] button:not(:disabled)' : '.playback-control-bar button:not(:disabled)')];
      if (!buttons.length) return;
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      if (arrows.includes(event.key)) {
        const delta = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 1;
        const next = Math.max(0, Math.min(buttons.length - 1, (index < 0 ? 0 : index) + delta));
        buttons[next].focus();
        buttons[next].scrollIntoView({block:'nearest'});
      } else if (!event.repeat) {
        const button = buttons[index < 0 ? 0 : index];
        button.focus();
        button.click();
      }
    };
    const keyup = (event: KeyboardEvent) => {
      if (expanded() && (event.key === ' ' || event.key === 'Enter') && rootRef.current?.contains(document.activeElement)) {
        event.preventDefault(); event.stopPropagation();
      }
    };
    window.addEventListener('keydown', keydown, true);
    window.addEventListener('keyup', keyup, true);
    return () => { window.removeEventListener('keydown', keydown, true); window.removeEventListener('keyup', keyup, true); };
  }, [large, onWakeControls, onHideControls, menu]);
  useEffect(() => {
    if (menu && (large || document.fullscreenElement?.contains(rootRef.current)))
      rootRef.current?.querySelector<HTMLButtonElement>('[role="menu"] button:not(:disabled)')?.focus();
  }, [menu, large]);
  useEffect(() => {
    if (!menu) return;
    const outside = (e: PointerEvent) => {
      const target = e.target as Node;
      if (settingsButtonRef.current?.contains(target)) return;
      if (!rootRef.current?.querySelector('[role="menu"]')?.contains(target)) setMenu(null);
    };
    window.addEventListener('pointerdown', outside, true);
    return () => window.removeEventListener('pointerdown', outside, true);
  }, [menu]);

  // One entry per language; a language with several tracks uses its first full one.
  const languages = [...new Set(p.tracks.map(trackKey))];
  const captionsOn = p.subtitleLanguage !== null && p.tracks.some(track => canonicalLanguage(trackKey(track)) === canonicalLanguage(p.subtitleLanguage!));
  const selectedAudio = p.audioTracks.find(track => track.index === p.audioTrackIndex);
  const toggle = (next: Menu) => setMenu((m) => (m ? null : next));
  const pipSupported = typeof document !== 'undefined' && document.pictureInPictureEnabled;

  return (
    <div className={'playback-controls' + (menu ? ' is-open' : '')} data-player-menu={menu ?? undefined} ref={rootRef} onClick={(e) => e.stopPropagation()}>
      {menu === 'audio' && (
        <div className="playback-menu" role="menu" aria-label="Audio Tracks">
          <button {...noFocus} role="menuitem" onClick={() => setMenu('settings')}>‹ Audio Tracks</button>
          <div className="playback-menu-list">
            {p.audioTracks.map(track => <button {...noFocus} key={track.index} role="menuitemradio" aria-checked={track.index === p.audioTrackIndex} onClick={() => { p.onAudioTrack(track); setMenu(null); }}>
              {languageLabel(canonicalLanguage(track.language || 'und'))} · {track.title || `Track ${track.index}`} · {track.codec.toUpperCase()} · {track.channels} ch
            </button>)}
          </div>
        </div>
      )}
      {menu === 'captions' && (
        <div className="playback-menu" role="menu" aria-label="Subtitles">
          <button {...noFocus} role="menuitem" onClick={() => setMenu('settings')}>
            ‹ Subtitles
          </button>
          <div className="playback-menu-list">
            <button
              {...noFocus}
              role="menuitemradio"
              aria-checked={!captionsOn}
              onClick={() => {
                p.onSubtitleLanguage(null);
                setMenu(null);
              }}
            >
              Off
            </button>
            {languages.map((l) => (
              <button
                {...noFocus}
                key={l}
                role="menuitemradio"
                aria-checked={p.subtitleLanguage === l}
                onClick={() => {
                  p.onSubtitleLanguage(l);
                  setMenu(null);
                }}
              >
                {languageLabel(l)}
              </button>
            ))}
          </div>
        </div>
      )}
      {menu === 'settings' && (
        <div className="playback-menu" role="menu" aria-label="Settings">
          <button {...noFocus} role="menuitem" onClick={() => setMenu('quality')}>
            Quality ({p.quality === 0 ? 'auto' : qualityLabel(p.quality)})
          </button>
          <button {...noFocus} role="menuitem" onClick={() => setMenu('audio')}>Audio Tracks ({playerLanguage(selectedAudio?.language)})</button>
          <button {...noFocus} role="menuitem" onClick={() => setMenu('captions')}>
            Subtitles ({captionsOn ? 'on' : 'off'})
          </button>
          {pipSupported && (
            <button
              {...noFocus}
              role="menuitem"
              onClick={() => {
                p.onPictureInPicture();
                setMenu(null);
              }}
            >
              Picture In Picture
            </button>
          )}
          <button
            {...noFocus}
            role="menuitemcheckbox"
            aria-checked={p.detailsOn}
            onClick={() => {
              p.onToggleDetails();
              setMenu(null);
            }}
          >
            Show Playback Details <span>{p.detailsOn ? 'On' : 'Off'}</span>
          </button>
        </div>
      )}
      {menu === 'quality' && (
        <div className="playback-menu" role="menu" aria-label="Quality">
          <button {...noFocus} role="menuitem" onClick={() => setMenu('settings')}>
            ‹ Quality
          </button>
          <div className="playback-menu-list">
            {[0, ...p.qualities].map((h) => (
              <button
                {...noFocus}
                key={h}
                role="menuitemradio"
                aria-checked={p.quality === h}
                onClick={() => {
                  p.onQuality(h);
                  setMenu(null);
                }}
              >
                {qualityLabel(h)}
              </button>
            ))}
          </div>
        </div>
      )}

      <ProgramOverlay session={p.session} />
      <div className="playback-control-bar">
      <button {...noFocus} className="playback-btn" aria-label={p.paused ? 'Play' : 'Pause'} title={p.paused ? 'Play (Space)' : 'Pause (Space)'} onClick={p.onTogglePlay}>
        <Icon>{p.paused ? <path d="M8 5v14l11-7z" /> : <path d="M6 5h4v14H6zm8 0h4v14h-4z" />}</Icon>
      </button>
      <span className="playback-time" aria-label="Elapsed And Total Time" title="Elapsed And Total Time">
        {formatClock(p.time)} / {formatClock(p.duration)}
      </span>
      <div className="playback-progress" aria-hidden="true">
        <div style={{ width: `${p.duration > 0 ? Math.min(100, (p.time / p.duration) * 100) : 0}%` }} />
      </div>
      <button
        {...noFocus}
        className="playback-btn"
        aria-label={p.muted || p.volume === 0 ? 'Unmute' : 'Mute'}
        title={p.muted || p.volume === 0 ? 'Unmute (m)' : 'Mute (m)'}
        onClick={p.onToggleMute}
      >
        <Icon>
          {p.muted || p.volume === 0 ? (
            <path d="M3 9v6h4l5 5V4L7 9H3zm13.6 3 2.4 2.4-1.4 1.4-2.4-2.4-2.4 2.4-1.4-1.4 2.4-2.4-2.4-2.4 1.4-1.4 2.4 2.4 2.4-2.4 1.4 1.4z" />
          ) : (
            <path d="M3 9v6h4l5 5V4L7 9H3zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4z" />
          )}
        </Icon>
      </button>
      {/* Range dragging needs its native mouse default; noFocus is buttons-only. */}
      <input
        className="playback-volume"
        type="range"
        min={0}
        max={1}
        step={0.05}
        value={p.muted ? 0 : p.volume}
        aria-label="Volume"
        title="Volume"
        onChange={(e) => p.onVolume(Number(e.target.value))}
        onPointerUp={(e) => e.currentTarget.blur()}
      />
      <button {...noFocus} ref={settingsButtonRef} className="playback-btn" aria-label="Settings" title="Settings" aria-haspopup="menu" onClick={() => toggle('settings')}>
        <Icon>
          <path d="M12 8a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm0 6a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm0 6a2 2 0 1 0 0-4 2 2 0 0 0 0 4z" />
        </Icon>
      </button>
      {p.onToggleLarge && (
        <button
          {...noFocus}
          className={'playback-btn' + (p.large ? ' is-on' : '')}
          aria-label={p.large ? 'Small Player' : 'Large Player'}
          title={p.large ? 'Small Player (t)' : 'Large Player (t)'}
          aria-pressed={p.large}
          onClick={p.onToggleLarge}
        >
          <Icon>
            {p.large ? (
              <path d="M5 16h3v3h2v-5H5v2zm3-8H5v2h5V5H8v3zm6 11h2v-3h3v-2h-5v5zm2-11V5h-2v5h5V8h-3z" />
            ) : (
              <path d="M4 4h16v16H4V4zm2 2v12h12V6H6z" />
            )}
          </Icon>
        </button>
      )}
      <button {...noFocus} className="playback-btn" aria-label="Full Screen" title="Full Screen (f)" onClick={p.onFullscreen}>
        <Icon>
          <path d="M5 5h5v2H7v3H5V5zm9 0h5v5h-2V7h-3V5zM5 14h2v3h3v2H5v-5zm12 0h2v5h-5v-2h3v-3z" />
        </Icon>
      </button>
      </div>
    </div>
  );
}
