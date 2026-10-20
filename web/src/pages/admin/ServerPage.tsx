import ServerDetails from './ServerDetails';
import LoudnessRow from './LoudnessRow';
import TlsControls, { type TlsControlsHandle } from './TlsControls';
import DdnsControls, { type DdnsControlsHandle } from './DdnsControls';
// One box of server settings in three columns: the setting's name, its control,
// and buttons for it (Test, Clear, Reset Default, More Info). More Info opens the
// setting's help in a centered modal.
//
// Hardware Encoding is in use: a hardware choice is tried by the server (see the
// Test button) and used only while that test passes. Hardware Decoding builds on
// it (it needs a hardware encoder) and moves decode, scale and tone mapping to the
// GPU, falling back to the CPU per file. Instance TLS and custom upload are active;
// DDNS has provider-specific write-only setup (#267); Let's Encrypt uses HTTP-01 (#242). Timezone is not configured
// here; set it with the TZ environment variable in docker-compose.yaml (it
// defaults to America/New_York).
//
import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  getHealth,
  getMetadataSettings,
  getServerSettings,
  restartServer,
  testTranscoding,
  updateMetadataSettings,
  updateServerSettings,
  type ServerSettings,
  type TranscodingTest,
} from '../../api/client';
import SettingsActions from './SettingsActions';
import InfoModal from './InfoModal';
import { reloadKeepingQuality } from '../../users/deviceStorage';
import { PasswordInput } from '../../components/PasswordInput';
import './AdminShared.css';

// TMDb's v4 "API Read Access Token" is a JWT: three dot-separated
// base64url segments, on the order of 200 characters — nothing like the
// short 32-character v3 "API Key" that sits right next to it on TMDb's
// own settings page (and won't work here). The length floor exists
// specifically to catch that mix-up, not just malformed input.
const TMDB_KEY_PATTERN = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/;
function isValidTmdbKey(value: string): boolean {
  return value.length >= 100 && TMDB_KEY_PATTERN.test(value);
}

const TRANSCODING_OPTIONS = [
  { value: 'software', label: 'Software Only (Default)' },
  { value: 'intel', label: 'Intel' },
  { value: 'amd-vaapi', label: 'AMD' },
  { value: 'nvidia-nvenc', label: 'NVIDIA' },
];

const DECODING_OPTIONS = [
  { value: 'off', label: 'Disabled (Default)' },
  { value: 'on', label: 'Enabled' },
];

const DEFAULTS: Pick<ServerSettings, 'transcoding' | 'hardwareDecoding' | 'tlsMode' | 'ddnsProvider'> = {
  transcoding: 'software',
  hardwareDecoding: false,
  tlsMode: 'self-signed',
  ddnsProvider: 'none',
};

type InfoKey = 'name' | 'encoding' | 'decoding' | 'tmdb' | 'tls';

// A grid row: the wrapper is display: contents, so the three cells (and an
// optional note beneath them) lay out on the shared grid.
function Row({ name, control, actions, note, sub, className = '' }: { name: ReactNode; control?: ReactNode; actions?: ReactNode; note?: ReactNode; sub?: boolean; className?: string }) {
  return (
    <div className={'setting-row' + (sub ? ' setting-sub' : '') + ' ' + className}>
      <div className="setting-name">{name}</div>
      <div className="setting-control">{control}</div>
      <div className="setting-actions">{actions}</div>
      {note}
    </div>
  );
}

const Button = ({ children, onClick, disabled, primary }: { children: ReactNode; onClick?: () => void; disabled?: boolean; primary?: boolean }) => (
  <button type="button" className={'admin-btn' + (primary ? '' : ' admin-btn-secondary')} onClick={onClick} disabled={disabled}>
    {children}
  </button>
);

// A saved key is never sent back from the server once it's set (see
// api/client.ts's MetadataSettingsStatus) — Inspect Element on this
// field will only ever show whatever the viewer just typed, never a
// previously-saved value. Set/Clear act immediately (their own PUT
// request) rather than waiting on the page's Save Changes button.
function TmdbKeyRow({ keySet, onInfo, onSet, onClear }: { keySet: boolean; onInfo: () => void; onSet: (value: string) => Promise<void>; onClear: () => Promise<void> }) {
  const [draft, setDraft] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const trimmed = draft.trim();

  const run = async (action: () => Promise<void>, failure: string) => {
    setBusy(true);
    setError(null);
    try {
      await action();
      setDraft('');
    } catch (err) {
      setError(err instanceof Error ? err.message : failure);
    } finally {
      setBusy(false);
    }
  };
  const handleSet = () => {
    if (!isValidTmdbKey(trimmed)) {
      setError('Doesn\'t look like a TMDb API Read Access Token (a long value with two "." separators) — see More Info. Not the short "API Key".');
      return;
    }
    void run(() => onSet(trimmed), 'Failed to save key');
  };

  return (
    <Row
      name="TMDb API Key"
      control={
        <PasswordInput
          revealDraftOnly
          visibilityLabel="API key"
          aria-label="TMDb API Key"
          value={draft}
          placeholder={keySet ? '•••••••••••••••• (set)' : 'Not configured'}
          onChange={(e) => {
            setDraft(e.target.value);
            setError(null);
          }}
        />
      }
      actions={
        <>
          {trimmed !== '' || !keySet ? (
            <Button primary onClick={handleSet} disabled={busy || trimmed === ''}>
              Set
            </Button>
          ) : (
            <Button onClick={() => void run(onClear, 'Failed to clear key')} disabled={busy}>
              Clear
            </Button>
          )}
          <Button onClick={onInfo}>More Info</Button>
        </>
      }
      note={error && <p className="profile-field-error setting-note">{error}</p>}
    />
  );
}

function Select({ value, options, onChange }: { value: string; options: { value: string; label: string }[]; onChange: (value: string) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

// The server keeps its process ID and comes back in a few seconds; the page
// reloads once it has gone down and answered again.
function RestartCard() {
  const [restarting, setRestarting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearInterval(timer.current), []);

  const restart = async () => {
    setError(null);
    try {
      await restartServer();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to restart the server');
      return;
    }
    setRestarting(true);
    const begun = Date.now();
    let wentDown = false;
    timer.current = window.setInterval(() => {
      getHealth().then(
        () => {
          if (wentDown || Date.now() - begun > 15000) reloadKeepingQuality();
        },
        () => {
          wentDown = true;
        },
      );
    }, 1000);
  };

  return (
    <div className="admin-card">
      <ServerDetails actions={
        <Button primary onClick={() => void restart()} disabled={restarting}>
          {restarting ? 'Restarting…' : 'Restart Server'}
        </Button>
      } />
      {error && <p className="profile-field-error setting-note">{error}</p>}
    </div>
  );
}

export default function ServerPage() {
  const tlsRef = useRef<TlsControlsHandle>(null);
  const [tlsDirty, setTlsDirty] = useState(false);
  const [ddnsHostname, setDdnsHostname] = useState<string | null>(null);
  const ddnsRef = useRef<DdnsControlsHandle>(null);
  const [ddnsDirty, setDdnsDirty] = useState(false);
  const [saved, setSaved] = useState<ServerSettings | null>(null);
  const [draft, setDraft] = useState<ServerSettings | null>(null);
  const [serverLoadError, setServerLoadError] = useState<string | null>(null);
  const [info, setInfo] = useState<InfoKey | null>(null);
  type TestState = { busy: boolean; result?: TranscodingTest; error?: string };
  const [test, setTest] = useState<TestState>({ busy: false });
  const [decodeTest, setDecodeTest] = useState<TestState>({ busy: false });

  useEffect(() => {
    getServerSettings()
      .then((s) => {
        setSaved(s);
        setDraft(s);
      })
      .catch((err: Error) => setServerLoadError(err.message));
  }, []);

  const change = (patch: Partial<ServerSettings>) => setDraft((d) => d && { ...d, ...patch });

  // Metadata settings are saved separately (GET/PUT /api/settings/metadata,
  // #77) and immediately, so they are left out of hasChanges/handleSave: seeing
  // them there would imply Save Changes/Cancel also govern them.
  const [metadataLoading, setMetadataLoading] = useState(true);
  const [metadataLoadError, setMetadataLoadError] = useState<string | null>(null);
  const [tmdbKeySet, setTmdbKeySet] = useState(false);
  const [showTlsCA, setShowTlsCA] = useState(false);

  useEffect(() => {
    getMetadataSettings()
      .then((s) => {
        setTmdbKeySet(s.tmdbApiKeySet);
        setMetadataLoadError(null);
      })
      .catch((err: Error) => setMetadataLoadError(err.message))
      .finally(() => setMetadataLoading(false));
  }, []);

  const hasChanges = tlsDirty || ddnsDirty || saved !== null && draft !== null && (Object.keys(saved) as (keyof ServerSettings)[]).some((k) => saved[k] !== draft[k]);

  const handleSave = async () => {
    await ddnsRef.current?.save();
    await tlsRef.current?.save();
    if (draft) await updateServerSettings({ serverName: draft.serverName, transcoding: draft.transcoding, hardwareDecoding: draft.hardwareDecoding,
      tlsMode: draft.tlsMode, acmeProvider: draft.acmeProvider, acmeDomain: draft.acmeDomain });
  };

  const runTest = async (decoding: boolean) => {
    if (!draft) return;
    const set = decoding ? setDecodeTest : setTest;
    set({ busy: true });
    try {
      set({ busy: false, result: await testTranscoding(draft.transcoding, decoding) });
    } catch (err) {
      set({ busy: false, error: err instanceof Error ? err.message : 'The test failed' });
    }
  };


  if (draft === null) {
    return (
      <div className="admin-section">
        <div className="admin-card">{serverLoadError ? <p className="profile-field-error">{serverLoadError}</p> : <p className="admin-section-hint">Loading…</p>}</div>
      </div>
    );
  }

  const noteFor = (t: TestState) =>
    t.busy ? (
      <p className="setting-note">Testing…</p>
    ) : t.error ? (
      <p className="profile-field-error setting-note">{t.error}</p>
    ) : t.result ? (
      <p className={t.result.ok ? 'setting-note setting-ok' : 'profile-field-error setting-note'}>
        {t.result.ok ? '✓ ' : '✗ '}
        {t.result.detail}
        {t.result.device ? ` (${t.result.device})` : ''}
      </p>
    ) : null;
  const testNote = noteFor(test);

  return (
    <div className="admin-section">
      <RestartCard />
      <div className="admin-card">
        <div className="settings-grid">
          <Row name="Server Name" control={<input aria-label="Server Name" type="text" value={draft.serverName} maxLength={64} required onChange={(e) => setDraft({ ...draft, serverName: e.target.value })} />} actions={<Button onClick={() => setInfo('name')}>More Info</Button>} />
          <Row
            name="Hardware Encoding"
            control={
              <Select
                value={draft.transcoding}
                options={TRANSCODING_OPTIONS}
                onChange={(transcoding) => {
                  // Decoding on the GPU needs a hardware encoder, so software takes it off.
                  change(transcoding === 'software' ? { transcoding, hardwareDecoding: false } : { transcoding });
                  setTest({ busy: false });
                  setDecodeTest({ busy: false });
                }}
              />
            }
            actions={
              <>
                {draft.transcoding !== 'software' && (
                  <Button onClick={() => void runTest(false)} disabled={test.busy}>
                    Test
                  </Button>
                )}
                <Button
                  onClick={() => {
                    change({ transcoding: DEFAULTS.transcoding, hardwareDecoding: false });
                    setTest({ busy: false });
                    setDecodeTest({ busy: false });
                  }}
                  disabled={draft.transcoding === DEFAULTS.transcoding}
                >
                  Reset Default
                </Button>
                <Button onClick={() => setInfo('encoding')}>More Info</Button>
              </>
            }
            note={testNote}
          />

          <Row
            name="Hardware Decoding"
            control={
              <select
                value={draft.hardwareDecoding ? 'on' : 'off'}
                disabled={draft.transcoding === 'software'}
                onChange={(e) => {
                  change({ hardwareDecoding: e.target.value === 'on' });
                  setDecodeTest({ busy: false });
                }}
              >
                {DECODING_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            }
            actions={
              <>
                {draft.hardwareDecoding && (
                  <Button onClick={() => void runTest(true)} disabled={decodeTest.busy}>
                    Test
                  </Button>
                )}
                <Button
                  onClick={() => {
                    change({ hardwareDecoding: DEFAULTS.hardwareDecoding });
                    setDecodeTest({ busy: false });
                  }}
                  disabled={!draft.hardwareDecoding}
                >
                  Reset Default
                </Button>
                <Button onClick={() => setInfo('decoding')}>More Info</Button>
              </>
            }
            note={draft.transcoding === 'software' ? <p className="setting-note">Choose a hardware encoder above first.</p> : noteFor(decodeTest)}
          />

          <LoudnessRow />

          <hr className="settings-divider" />

          {metadataLoading ? (
            <Row name="TMDb API Key" control={<span className="admin-section-hint">Loading…</span>} />
          ) : (
            <>
              <TmdbKeyRow
                keySet={tmdbKeySet}
                onInfo={() => setInfo('tmdb')}
                onSet={async (value) => {
                  const s = await updateMetadataSettings({ tmdbApiKey: value });
                  setTmdbKeySet(s.tmdbApiKeySet);
                          }}
                onClear={async () => setTmdbKeySet((await updateMetadataSettings({ tmdbApiKey: '' })).tmdbApiKeySet)}
              />
              {metadataLoadError && <p className="profile-field-error setting-note">{metadataLoadError}</p>}

            </>
          )}

          <hr className="settings-divider" />

          <DdnsControls ref={ddnsRef} onDirty={setDdnsDirty} onHostname={setDdnsHostname} />

          <hr className="settings-divider" />

          <TlsControls ref={tlsRef} onDirty={setTlsDirty} ddnsHostname={ddnsHostname} onCADownloadChange={setShowTlsCA}
            actions={
              <>
                {showTlsCA && (
                  <a className="admin-btn admin-btn-secondary" href="/api/settings/tls/ca.pem" download="alreadyon-instance-ca.pem">
                    CA Certificate
                  </a>
                )}
                <Button onClick={() => setInfo('tls')}>More Info</Button>
              </>
            }
          />

        </div>
      </div>

      <SettingsActions hasChanges={hasChanges} onSave={handleSave} />

      <InfoModal open={info === 'name'} title="Server Name" onClose={() => setInfo(null)}>
        <p>Choose a recognizable name for this AlreadyOn server. Phones, tablets and TVs show this name when searching your home network, so you can tell servers apart before signing in. Save a new name to update what clients see during discovery.</p>
      </InfoModal>
      <InfoModal open={info === 'encoding'} title="Hardware Encoding" onClose={() => setInfo(null)}>
        <p>
          Converting a video the browser can't play (or that is too large for the connection) is the most demanding thing the server does. <strong>Software Only</strong>{' '}
          encodes on the CPU and works everywhere. A hardware choice moves the video <em>encoding</em> (the compressing of the finished stream) to a GPU, which uses
          much less CPU.
        </p>
        <p>
          Picking one assumes the drivers are installed on the host and the device is passed into the container. AlreadyOn tests the choice with a one-second
          encode (the Test button, and again on its own every ten minutes): while the test passes, streams use the hardware, and when it fails they quietly use
          software, so a wrong choice never stops playback. A stream that fails on the hardware also switches the next ones to software for a minute.
        </p>
        <ul>
          <li>
            <strong>Intel</strong>: an Intel GPU and the full Intel media driver on the host. The server uses Quick Sync (QSV) when it works fully (the encode test, and with Hardware
            Decoding on the whole-GPU test too) and falls back to VA-API otherwise. Quick Sync needs an Intel 11th-generation (Tiger Lake) or newer GPU, or Arc; older Intel GPUs use VA-API. The
            Test button says which one streams will use.
          </li>
          <li>
            <strong>AMD</strong>: an AMD GPU with Mesa's VA-API drivers.
          </li>
          <li>
            <strong>NVIDIA</strong>: an NVIDIA GPU, its driver, and the NVIDIA Container Toolkit.
          </li>
        </ul>
        <p>
          Intel and AMD need the render device in docker-compose.yaml. The container gives the server that device's group itself, so PUID and PGID can stay as they
          are:
        </p>
        <pre>{`devices:
  - /dev/dri:/dev/dri`}</pre>
        <p>NVIDIA needs the toolkit's GPU reservation instead:</p>
        <pre>{`deploy:
  resources:
    reservations:
      devices:
        - driver: nvidia
          count: 1
          capabilities: [gpu, video]`}</pre>
        <p>
          With only Hardware Encoding, decoding, scaling and HDR tone mapping still run on the CPU (see Hardware Decoding), and the optional two-rung ladder
          (Playback settings API) is software only. Videos that play directly are never transcoded.
        </p>
      </InfoModal>

      <InfoModal open={info === 'decoding'} title="Hardware Decoding" onClose={() => setInfo(null)}>
        <p>
          Before encoding, the server has to <em>decode</em> your file back into raw pictures, resize them to the streaming size and, for HDR video, convert the
          colors to normal (SDR) ones (<em>tone mapping</em>). Hardware Decoding moves all three to the GPU too, so the pictures stay on the GPU from start to
          finish and the CPU barely works. The gain is largest for 4K, HEVC and HDR files.
        </p>
        <p>
          <strong>It needs a hardware encoder.</strong> The option stays off until you pick one under Hardware Encoding, and choosing Software Only there turns it
          off again. With a software encoder the pictures would have to be copied back to the CPU anyway, which loses the benefit.
        </p>
        <p>
          Each file is checked against what a GPU can decode (codec, 10-bit, size). A file it can't handle is decoded on the CPU, and the GPU still encodes. If
          GPU decoding fails or stalls while starting, the stream retries on the CPU, so playback never stops because of it, and that kind of video stays on the
          CPU for ten minutes.
        </p>
        <p>
          <strong>HDR tone mapping</strong> depends on the GPU. <strong>Intel</strong> (Quick Sync or VA-API) does it on the GPU for HDR10 video. AMD and NVIDIA
          GPUs, and HLG video, tone map on the CPU, at the reduced size, after the GPU has decoded and scaled. The GPU's colors differ a little from the CPU's,
          and are slightly darker.
        </p>
        <p>
          When it is Enabled, the Test button runs a short H.264 clip through the whole GPU path (decode, scale and encode) and says whether it worked. It
          doesn't test other codecs (HEVC, AV1, 10-bit); a file that fails on the GPU falls back as above. Show Playback Details in the player menu shows where
          each step ran for the stream you are watching.
        </p>
      </InfoModal>

      <InfoModal open={info === 'tmdb'} title="TMDb API Key" onClose={() => setInfo(null)}>
        <p>
          Used to classify library content by genre and fetch overview, date and certification details. TMDb is the best source and needs a free account and API
          key. Set and Clear take effect immediately; they don't wait for Save Changes.
        </p>
        <p>
          Create a free account at{' '}
          <a href="https://www.themoviedb.org" target="_blank" rel="noreferrer">
            themoviedb.org
          </a>
          , then request a key from{' '}
          <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer">
            Settings → API
          </a>{' '}
          (you'll need to accept their terms of use; TMDb's own{' '}
          <a href="https://developer.themoviedb.org/docs/getting-started" target="_blank" rel="noreferrer">
            getting-started guide
          </a>{' '}
          has the full walkthrough).
        </p>
        <p>
          That page shows <em>two</em> credentials — a short "API Key" and a long "API Read Access Token". <strong>Use the long API Read Access Token</strong>; the
          short one won't work here.
        </p>
        <p>This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
      </InfoModal>

      <InfoModal open={info === 'tls'} title="TLS Certificates" onClose={() => setInfo(null)}>
        <p>
          TLS certificates encrypt the connection between your devices and AlreadyOn, helping keep sign-ins and video traffic private. A trusted certificate
          also helps your device confirm it is talking to the right server.
        </p>
        <p>
          <strong>Self-Signed (Default)</strong> — AlreadyOn creates its own unique certificate automatically, with a ten-year initial lifetime, so encryption
          works without setup. Use the CA Certificate button beside More Info and import the download on each device to trust this server. Verify the CA fingerprint
          through your server terminal before trusting it; a certificate warning alone does not establish identity.
        </p>
        <p>
          Start with your browser's certificate settings and import the downloaded CA as a trusted authority for identifying websites, not as a personal
          certificate. Search for browser-specific import instructions:{' '}
          <a href="https://www.google.com/search?q=Microsoft+Edge+import+trusted+root+CA+certificate" target="_blank" rel="noreferrer">
            Microsoft Edge
          </a>
          {' · '}
          <a href="https://www.google.com/search?q=Google+Chrome+import+trusted+root+CA+certificate+certificate+manager" target="_blank" rel="noreferrer">
            Google Chrome
          </a>
          {' · '}
          <a href="https://www.google.com/search?q=Mozilla+Firefox+import+trusted+root+CA+certificate+Authorities" target="_blank" rel="noreferrer">
            Mozilla Firefox
          </a>
          . Chrome provides a Certificate Manager; Firefox provides an Authorities import. Edge may use your operating system's certificate store instead.
          Available controls depend on your browser version and device.
        </p>
        <p>
          If browser import is unavailable or does not resolve the trust warning, import the CA into your operating system's trusted certificate store, then
          restart the browser. OS fallback guides:{' '}
          <a
            href="https://learn.microsoft.com/en-us/windows-hardware/drivers/install/trusted-root-certification-authorities-certificate-store"
            target="_blank"
            rel="noreferrer"
          >
            Windows
          </a>
          {' · '}
          <a href="https://support.apple.com/guide/keychain-access/change-the-trust-settings-of-a-certificate-kyca11871/mac" target="_blank" rel="noreferrer">
            macOS
          </a>
          {' · '}
          <a
            href="https://documentation.ubuntu.com/server/how-to/security/install-a-root-ca-certificate-in-the-trust-store/index.html"
            target="_blank"
            rel="noreferrer"
          >
            Linux (Ubuntu)
          </a>
          . Some browsers may still require their own authority import or permission to trust OS-installed certificates. Do not disable certificate checks.
        </p>
        <p>
          <strong>User Uploaded Cert</strong> — Use a certificate created outside AlreadyOn, issued by a CA your devices trust and containing the DNS names you
          intend to use. Upload the PEM certificate chain, matching unencrypted PEM private key and issuing CA bundle, then click Save Changes. It
          applies to UI, API and playback on the shared HTTPS listener immediately and survives restarts; renew it before it expires. PFX/P12 bundles must be converted to PEM externally. Your private
          key is never available for download.
        </p>
        <p>
          <strong>Let's Encrypt</strong> — Public certificates use HTTP-01 on WAN TCP/80, forwarded to the dedicated challenge port 19902 published in Compose. DDNS is optional. Enter the public hostname and contact email, accept the subscriber terms and click Save Changes. Staging validation runs automatically when needed before production issuance. Status refreshes every five minutes. Renewal is automatic; failure retains the current certificate.
        </p>
        <p>
          <a href="https://letsencrypt.org/docs/challenge-types/#http-01-challenge" target="_blank" rel="noreferrer">Let's Encrypt HTTP-01 setup</a>
        </p>
      </InfoModal>


    </div>
  );
}
