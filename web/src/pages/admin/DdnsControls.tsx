import { useEffect, useState, useImperativeHandle, forwardRef, type ReactNode } from 'react';
import { getDdnsStatus, saveDdns, type DdnsStatus, type DdnsConfig } from '../../api/client';
import { PasswordInput } from '../../components/PasswordInput';
import InfoModal from './InfoModal';

const providers = [
  { value: 'none', label: 'None' }, { value: 'cloudflare', label: 'Cloudflare' },
  { value: 'no-ip', label: 'No-IP' },
];
const empty = (provider: string): DdnsConfig => ({ provider, hostname: '', zoneId: '', username: '', hasSecret: false, enabled: false, lastAttempt: 0, lastUpdate: 0, lastVerified: 0, lastIp: '', lastError: '', nextAttempt: 0, blocked: false });
const date = (unix: number) => unix ? new Date(unix * 1000).toLocaleString() : 'Not yet';
const link = (url: string, text: string) => <a href={url} target="_blank" rel="noreferrer">{text}</a>;

// Provider-specific help follows the draft selection, before any network writes.
function ProviderHelp({ provider }: { provider: string }) {
  if (provider === 'none') return <>
    <p>Dynamic DNS (DDNS) keeps a public hostname pointed at your home internet connection when its public IP address changes. None disables AlreadyOn's updater; you can still use an existing router or external DDNS service.</p>
    <p>To reach AlreadyOn from the internet, forward the shared HTTPS application port through your home router to this server, and allow them through your firewall. DDNS does not open ports. Carrier-grade NAT or a second router may prevent incoming connections.</p>
    <p>We recommend enabling a Let's Encrypt ACME certificate when exposing the service to the internet. Its HTTP validation requires publishing container TCP/19902 in Compose and forwarding public TCP/80 to that challenge port through your router. Create your account privately and use a strong, unique administrator password.</p>
  </>;
  return <>
    {provider === 'cloudflare' && <>
      <ol>
        <li>{link('https://dash.cloudflare.com/sign-up', 'Create a Cloudflare account')} and add a domain you own. Follow its nameserver instructions until the zone is active. DNS hosting has a free plan; domain registration is separate.</li>
        <li>AlreadyOn creates a missing A record, or updates the existing A record for your hostname. It always enforces <strong>DNS only</strong>, including switching existing proxied records off. Existing TTL and other metadata are preserved.</li>
        <li>{link('https://developers.cloudflare.com/fundamentals/api/get-started/create-token/', 'Create a scoped API token')} with Zone → DNS → Edit and Zone → Zone → Read restricted to that one zone. AlreadyOn discovers the zone automatically.</li>
        <li>Enter the full hostname and API token below, then click Save Changes.</li>
      </ol>
    </>}
    {provider === 'no-ip' && <>
      <ol>
        <li>{link('https://www.noip.com/free', 'Create a No-IP account')} and create a DNS Host (A) hostname. Do not select a URL/port redirect.</li>
        <li>Create a {link('https://www.noip.com/support/knowledgebase/how-to-setup-and-use-a-ddns-key', 'DDNS Key')} for the hostname if available. Enter its generated username/password and the actual full hostname, not the all.ddnskey.com group alias. Account update credentials also work where supported.</li>
        <li>Click Save Changes. Avoid running a second updater for the same hostname.</li>
        <li>{link('https://www.noip.com/support/knowledgebase/confirm-my-hostname-free-account-support-question-day', 'Confirm your free hostname every 30 days')}; DNS updates do not replace this account confirmation.</li>
      </ol>
    </>}
    <p>Save Changes stores this provider's setup and performs an initial sync. Automatic checks run every five minutes. Cloudflare records are written only when the address or DNS-only setting needs correction. DNS propagation may take time; failures use retry backoff.</p>
    <p>DDNS does not make the application reachable by itself. Forward your shared HTTPS application port through the router/firewall and use an ACME certificate for public access. This initial updater manages IPv4 A records; check any existing IPv6 AAAA record separately.</p>
  </>;
}

// Each provider's saved credential stays server-side. Switching providers erases
// typed secret input and restores only non-secret setup for the new provider.
export type DdnsControlsHandle = { save: () => Promise<void> };
export default forwardRef<DdnsControlsHandle, { onDirty: (dirty: boolean) => void; onHostname: (hostname: string | null) => void }>(function DdnsControls({ onDirty, onHostname }, ref) {
  const [status, setStatus] = useState<DdnsStatus | null>(null);
  const [provider, setProvider] = useState('none');
  const [draft, setDraft] = useState<DdnsConfig>(empty('none'));
  const [secret, setSecret] = useState('');
  const [busy, setBusy] = useState(false);
  const [help, setHelp] = useState(false);
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  const [pollError, setPollError] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    let active = true;
    getDdnsStatus().then(s => { if (!active) return; setStatus(s); setProvider(s.provider); setDraft(s.configs.find(c => c.provider === s.provider) ?? empty(s.provider)); }).catch(() => { if (active) setError('Could not load DDNS settings.'); });
    const timer = setInterval(() => { setNow(Math.floor(Date.now() / 1000)); if (document.visibilityState === 'visible') getDdnsStatus().then(s => { if (active) { setStatus(s); setPollError(false); } }).catch(() => { if (active) setPollError(true); }); }, 5000);
    return () => { active = false; clearInterval(timer); };
  }, []);
  useEffect(() => { onHostname(provider === 'none' ? null : draft.hostname); }, [provider, draft.hostname, onHostname]);
  const selected = status?.configs.find(c => c.provider === provider) ?? empty(provider);
  const dirty = provider !== status?.provider || secret !== '' || ['hostname', 'username'].some(k => draft[k as keyof DdnsConfig] !== selected[k as keyof DdnsConfig]);
  const running = !!status?.running;
  const choose = (value: string) => { setProvider(value); setDraft(status?.configs.find(c => c.provider === value) ?? empty(value)); setSecret(''); setError(''); };
  useEffect(() => { onDirty(!!status && dirty && !busy && !running); }, [status, dirty, busy, running, onDirty]);
  useImperativeHandle(ref, () => ({ save: async () => {
    if (!dirty) return;
    if (!status || busy || running) throw new Error('DDNS settings are not ready to save.');
    setBusy(true);
    try {
      const s = await saveDdns({ provider, hostname: draft.hostname, username: draft.username, enabled: provider !== 'none', ...(secret ? { secret } : {}) });
      setStatus(s); setSecret(''); setDraft(s.configs.find(c => c.provider === provider) ?? empty(provider));
    } finally { setBusy(false); }
  } }));
  const row = (name: string, control: ReactNode, actions?: ReactNode, sub = true) => <div className={'setting-row' + (sub ? ' setting-sub' : '')}><div className="setting-name">{name}</div><div className="setting-control">{control}</div><div className="setting-actions">{actions}</div></div>;
  const btn = (text: string, onClick: () => void, disabled = false) => <button type="button" className="admin-btn admin-btn-secondary" onClick={onClick} disabled={disabled}>{text}</button>;
  const field = (label: string, key: 'hostname' | 'zoneId' | 'username', placeholder: string) => row(label, <input type="text" aria-label={label} value={draft[key]} placeholder={placeholder} disabled={busy || running} autoComplete="off" onChange={e => setDraft({ ...draft, [key]: e.target.value })} />, key === 'hostname' ? <div role="status">{dirty && <>Unsaved DDNS changes. </>}{status?.running === provider ? <>Sync in progress… · {date(selected.lastAttempt)}</> : selected.lastAttempt ? <>Last sync: {selected.lastError ? 'Failed' : 'Success'} · {date(selected.lastAttempt)}{selected.lastError && <> · {selected.lastError}</>}</> : 'No sync attempted yet.'}{selected.blocked ? <><br />Updates stopped; correct provider setup and save.</> : selected.lastError && selected.nextAttempt > now ? <><br />Next retry: {date(selected.nextAttempt)}.</> : null}{pollError && <><br />Status refresh failed; showing the last received result.</>}</div> : undefined);
  return <>
    {row('Dynamic DNS', <select aria-label="DDNS provider" value={provider} onChange={e => choose(e.target.value)} disabled={!status || busy || running}>{providers.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}</select>, <>{btn('Reset Default', () => choose('none'), provider === 'none' || busy || running)}{btn('More Info', () => setHelp(true))}</>, false)}

    {provider !== 'none' && <>
      {field('Public Hostname', 'hostname', 'tv.example.com')}
      {provider === 'no-ip' && field('DDNS Username', 'username', 'Provider update username')}
      {row(provider === 'cloudflare' ? 'API Token' : 'DDNS Password', <PasswordInput revealDraftOnly visibilityLabel="credential" key={provider} aria-label="DDNS credential" value={secret} onChange={e => setSecret(e.target.value)} autoComplete="new-password" placeholder={selected.hasSecret ? '•••••••••••••••• (set)' : 'Enter provider credential'} disabled={busy || running} />)}

    </>}
    {error && <p className="setting-note profile-field-error" role="alert">{error}</p>}
    <InfoModal open={help} title={provider === 'none' ? 'Dynamic DNS' : `${providers.find(p => p.value === provider)?.label} Setup`} onClose={() => setHelp(false)}><ProviderHelp provider={provider} /></InfoModal>
  </>;
});
