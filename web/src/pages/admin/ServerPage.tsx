import ServerDetails from './ServerDetails';
import LoudnessRow from './LoudnessRow';
import TlsControls, { type TlsControlsHandle } from './TlsControls';
import DdnsControls, { type DdnsControlsHandle } from './DdnsControls';
// One box of server settings in three columns: the setting's name, its control,
// and its immediate actions. More Info opens the setting's help in a centered
// modal.
//
// The vendor choice controls the whole playback pipeline; calibration selects
// working GPU stages and retains software fallbacks.
import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  getHealth,
  getMetadataSettings,
  getServerSettings,
  restartServer,
  getBenchmarkStatus,
  runFullBenchmark,
  runQuickBenchmark,
  stopBenchmark,
  type BenchmarkStatus,
  updateMetadataSettings,
  updateServerSettings,
  type ServerSettings,
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
  { value: 'software', label: 'CPU Only (Default)' },
  { value: 'intel', label: 'Intel' },
  { value: 'amd-vaapi', label: 'AMD' },
  { value: 'nvidia-nvenc', label: 'NVIDIA' },
];

type InfoKey = 'name' | 'encoding' | 'benchmark-results' | 'tmdb' | 'tls';

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
    const health = { wentDown: false };
    timer.current = window.setInterval(() => {
      getHealth().then(
        () => {
          if (health.wentDown || Date.now() - begun > 15000) reloadKeepingQuality();
        },
        () => {
          health.wentDown = true;
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
  const [benchmark, setBenchmark] = useState<BenchmarkStatus | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [requesting, setRequesting] = useState(false);
  const [confirmFullTest, setConfirmFullTest] = useState(false);
  const [dismissedRecommendation, setDismissedRecommendation] = useState('');
  useEffect(() => {
    let active = true;
    const poll = () => { void getBenchmarkStatus().then(s => { if (active) setBenchmark(s); }).catch(() => {}); };
    poll(); const timer = window.setInterval(poll, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);

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

  const selectVendor = async (transcoding: string) => {
    if (!draft) return;
    setRequesting(true); setTestError(null);
    try {
      const result = await updateServerSettings({ transcoding });
      setSaved(old => old && { ...old, transcoding: result.transcoding, hardwareDecoding: result.hardwareDecoding });
      change({ transcoding: result.transcoding, hardwareDecoding: result.hardwareDecoding });
      setBenchmark(await getBenchmarkStatus());
    } catch (err) { setTestError(err instanceof Error ? err.message : 'Could not save processing option'); }
    finally { setRequesting(false); }
  };
  const startTest = async (mode: 'quick' | 'full', allowActiveStreams = false) => {
    setRequesting(true); setTestError(null);
    try {
      if (mode === 'quick') {
        setBenchmark(await runQuickBenchmark());
      } else {
        setBenchmark(await runFullBenchmark(allowActiveStreams));
      }
      setConfirmFullTest(false);
    }
    catch (err) { setTestError(err instanceof Error ? err.message : `Could not start ${mode} test`); }
    finally { setRequesting(false); }
  };
  const requestFullTest = async () => {
    setTestError(null);
    setRequesting(true);
    try {
      const status = await getBenchmarkStatus();
      setBenchmark(status);
      if (status.activeStreams > 0) {
        setConfirmFullTest(true);
      } else {
        setBenchmark(await runFullBenchmark());
      }
    } catch (err) {
      setTestError(err instanceof Error ? err.message : 'Could not start full test');
    } finally {
      setRequesting(false);
    }
  };
  const stopTest = async () => {
    setRequesting(true); setTestError(null);
    try { setBenchmark(await stopBenchmark()); }
    catch (err) { setTestError(err instanceof Error ? err.message : 'Could not stop test'); }
    finally { setRequesting(false); }
  };
  const testRunning = benchmark?.state === 'queued' || benchmark?.state === 'running';
  const testResults = benchmark?.results ?? [];
  const recommendationKey = benchmark?.quickAt ?? '';
  const progress = (() => {
    if (!benchmark || !testRunning) return '';
    if (benchmark.state === 'queued') return `Waiting to start the ${benchmark.mode === 'mini' ? 'quick' : 'full'} test…`;
    const task = benchmark.tasks.find(candidate => candidate.name === 'Pipeline analysis' && candidate.total > 0);
    if (!task) return `Preparing the ${benchmark.mode === 'mini' ? 'quick' : 'full'} video transcoding test…`;
    const current = Math.min(task.total, task.completed + 1);
    const percent = Math.round(current * 100 / task.total);
    const name = task.currentFile || 'Video transcoding pipeline';
    return `${current} out of ${task.total} test: ${name} (${percent}%)`;
  })();


  if (draft === null) {
    return (
      <div className="admin-section">
        <div className="admin-card">{serverLoadError ? <p className="profile-field-error">{serverLoadError}</p> : <p className="admin-section-hint">Loading…</p>}</div>
      </div>
    );
  }


  return (
    <div className="admin-section">
      <RestartCard />
      <div className="admin-card">
        <div className="settings-grid">
          <Row name="Server Name" control={<input aria-label="Server Name" type="text" value={draft.serverName} maxLength={64} required onChange={(e) => setDraft({ ...draft, serverName: e.target.value })} />} actions={<Button onClick={() => setInfo('name')}>More Info</Button>} />
          <Row name="Video Transcoding"
            control={<select aria-label="Video Transcoding" value={draft.transcoding} disabled={requesting || testRunning}
              onChange={e => void selectVendor(e.target.value)}>
              {TRANSCODING_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>}
            actions={<>
              {testRunning ? (
                <Button primary onClick={() => void stopTest()} disabled={requesting}>Stop Test</Button>
              ) : (
                <>
                  <Button onClick={() => void startTest('quick')} disabled={requesting}>Quick Test</Button>
                  <Button onClick={() => void requestFullTest()} disabled={requesting}>Full Test</Button>
                </>
              )}
              <Button onClick={() => setInfo('encoding')}>More Info</Button>
            </>}
            note={(testRunning || testResults.length > 0) && (
              <div className="setting-note">
                {testResults.length > 0 && (
                  <button type="button" className="benchmark-results-link" onClick={() => setInfo('benchmark-results')}>
                    See Test Results
                  </button>
                )}
                {testRunning && <p role="status" aria-live="polite">{progress}</p>}
              </div>
            )}
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
      <InfoModal open={info === 'encoding'} title="Video Transcoding" onClose={() => setInfo(null)}>
        <p>CPU Only is the default. Intel, AMD and NVIDIA let AlreadyOn test compatible GPU decoding, scaling, HDR tone mapping and encoding, retaining CPU fallbacks for unsupported sources.</p>
        <p>Quick Test pauses streaming and background work, then runs a check limited to two minutes. Full Test pauses background work and asks before interrupting active streams. Stop Test preserves completed cases and removes the temporary files from incomplete work. Progress refreshes every five seconds.</p>
        <p>Startup skips the quick check when compatible results already exist. Manual quick results do not replace full-test results. Intel tries Quick Sync and VA-API with compatible runtimes; OpenCL provides an alternative GPU tone mapper when available.</p>
        <p>Intel and AMD need /dev/dri passed into the container. NVIDIA needs its host driver and NVIDIA Container Toolkit. Direct playback avoids conversion. Playback Details shows which stages the current stream actually uses.</p>
      </InfoModal>

      <InfoModal open={info === 'benchmark-results'} title="Video Transcoding Test Results"
        className="benchmark-results-dialog" onClose={() => setInfo(null)}>
        <div className="modal-table-scroll" tabIndex={0} role="region" aria-label="Test results">
          <table className="admin-table benchmark-results-table">
            <thead><tr><th scope="col">Test Name</th><th scope="col">Multiplier</th><th scope="col">Result</th></tr></thead>
            <tbody>
              {testResults.map((test, index) => (
                <tr key={`${test.name}:${index}`}>
                  <th scope="row">{test.name}</th>
                  <td>{test.multiplier > 0 ? `x${test.multiplier.toFixed(2)}` : '—'}</td>
                  <td>{test.passed ? 'Pass' : 'Fail'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </InfoModal>

      <InfoModal open={confirmFullTest} title="Active Streams Detected" onClose={() => setConfirmFullTest(false)}>
        <p>Active streams have been detected and will be paused while the full test runs. Select Test Anyway to pause all streams and background tasks, or Cancel to keep playback running.</p>
        {testError && <p className="profile-field-error">{testError}</p>}
        <div className="admin-modal-actions">
          <Button onClick={() => setConfirmFullTest(false)} disabled={requesting}>Cancel</Button>
          <Button primary onClick={() => void startTest('full', true)} disabled={requesting}>Test Anyway</Button>
        </div>
      </InfoModal>

      <InfoModal open={Boolean(testError) && !confirmFullTest} title="Video Transcoding Test" onClose={() => setTestError(null)}>
        <p className="profile-field-error">{testError}</p>
      </InfoModal>

      <InfoModal open={Boolean(benchmark?.recommendFull) && benchmark?.manual === true && benchmark?.mode === 'mini' && (benchmark?.outcome === 'completed' || benchmark?.outcome === 'time-limit') && recommendationKey !== dismissedRecommendation && !testRunning && !testError && !confirmFullTest && info === null} title="Full Test Recommended" onClose={() => setDismissedRecommendation(recommendationKey)}>
        <p>Quick Test results differ substantially from the previous Full Test. Run a Full Test to refresh the server settings.</p>
        <div className="admin-modal-actions">
          <Button onClick={() => setDismissedRecommendation(recommendationKey)}>Cancel</Button>
          <Button primary onClick={() => void requestFullTest()} disabled={requesting}>Run Full Test</Button>
        </div>
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
