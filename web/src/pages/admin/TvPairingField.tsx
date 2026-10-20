// Pairing secrets stay masked until the user explicitly reveals the entered PIN.
import { PasswordInput } from '../../components/PasswordInput';
import { useTvPairing } from './useTvPairing';

const pairingHelp = 'A 6–12 digit PIN used with your username to sign in to AlreadyOn on a TV over your local network, without entering your account password. Profile PINs still apply. Changing this PIN affects new sign-ins only.';

export default function TvPairingField({ pairing, disabled }: { pairing: ReturnType<typeof useTvPairing>; disabled: boolean }) {
  return <>
    <div className="admin-field-row">
      <label htmlFor="tv-pairing-pin" title={pairingHelp} style={{ cursor: 'help' }}>TV Pairing Pin</label>
      <PasswordInput id="tv-pairing-pin" visibilityLabel="PIN" aria-description={pairingHelp} inputMode="numeric" autoComplete="new-password"
        minLength={6} maxLength={12} pattern="[0-9]{6,12}" value={pairing.pin}
        placeholder={pairing.enabled ? '••••••' : undefined}
        onChange={event => pairing.setPin(event.target.value.replace(/[^0-9]/g, '').slice(0, 12))} disabled={disabled || pairing.busy} />
      {pairing.enabled && <button className="admin-btn admin-btn-secondary" type="button" disabled={disabled || pairing.busy} onClick={() => void pairing.remove()}>Remove Pin</button>}
    </div>
    {pairing.error && <p className="profile-field-error" role="alert">{pairing.error}</p>}
  </>;
}
