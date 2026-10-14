// Shared password entry keeps visibility controls and their accessible state consistent.
import { useState, type InputHTMLAttributes } from 'react';
import './PasswordInput.css';

type PasswordInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  allowAutofill?: boolean;
  visibilityLabel?: string;
  /** Saved API credentials are placeholders; only a nonempty replacement draft is revealable. */
  revealDraftOnly?: boolean;
  /** Render as a masked text field so extensions such as Bitwarden, which ignore
   *  autocomplete hints, find no password field to fill on page load. */
  concealFromManagers?: boolean;
};

// Browsers without -webkit-text-security would show the text, so they keep type="password".
const canMaskText = typeof CSS !== 'undefined' && CSS.supports?.('-webkit-text-security', 'disc');

export function PasswordInput({ allowAutofill = false, concealFromManagers = false, visibilityLabel = 'Password', revealDraftOnly = false, ...props }: PasswordInputProps) {
  const [shown, setVisible] = useState(false);
  const canReveal = !revealDraftOnly || String(props.value ?? '').length > 0;
  const visible = shown && canReveal;
  const toggleLabel = `${visible ? 'Hide' : 'Show'} ${visibilityLabel}`;
  const masked = concealFromManagers && canMaskText && !visible;
  const type = visible || masked ? 'text' : 'password';

  return (
    <span className="password-input-wrap">
      {/* Settings secrets must not be mistaken for the site's login credential.
          Extensions may ignore these hints; the login screen explicitly opts in. */}
      <input {...props} onChange={(event) => {
        // A new draft after clearing/saving starts masked, even if the last draft was shown.
        if (revealDraftOnly && (!canReveal || event.target.value === '')) setVisible(false);
        props.onChange?.(event);
      }} autoComplete={allowAutofill ? props.autoComplete : 'off'} data-lpignore={allowAutofill ? undefined : 'true'} data-1p-ignore={allowAutofill ? undefined : 'true'} data-bwignore={allowAutofill ? undefined : 'true'} data-form-type={allowAutofill ? undefined : 'other'} className={['password-input-control', masked && 'password-input-masked', props.className].filter(Boolean).join(' ')} type={type} />
      {canReveal && <button
        className="password-input-toggle"
        type="button"
        disabled={props.disabled}
        aria-label={toggleLabel}
        aria-pressed={visible}
        aria-controls={props.id}
        title={toggleLabel}
        onClick={() => setVisible((shown) => !shown)}
      >
        {/* The icon shows the current state: a slashed eye while hidden. */}
        <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
          {!visible ? (
            <>
              <path d="M3 3l18 18M10.6 10.6a2 2 0 0 0 2.8 2.8" />
              <path d="M9.9 5.2A10.8 10.8 0 0 1 12 5c5 0 8.5 4.5 9.5 7-.4 1-1.2 2.1-2.3 3.1M6.2 6.2C3.9 7.6 2.8 9.5 2.5 12c1 2.5 4.5 7 9.5 7 1.4 0 2.7-.4 3.8-1" />
            </>
          ) : (
            <>
              <path d="M2.5 12c1-2.5 4.5-7 9.5-7s8.5 4.5 9.5 7c-1 2.5-4.5 7-9.5 7s-8.5-4.5-9.5-7z" />
              <circle cx="12" cy="12" r="2.5" />
            </>
          )}
        </svg>
      </button>}
    </span>
  );
}
