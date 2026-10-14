import { useLayoutEffect } from 'react';

const selector = 'form, input, textarea, select';
const ignoredTypes = new Set(['checkbox', 'radio', 'range', 'file', 'hidden', 'button', 'submit', 'reset', 'color']);

// Suppression applies to dynamically mounted dialogs as well as every route.
// Only the explicitly marked sign-in form is allowed to advertise login fields.
export default function AutofillPolicy() {
  useLayoutEffect(() => {
    const accepted = new WeakMap<HTMLInputElement | HTMLTextAreaElement, string>();
    const exempt = (element: Element) => !!element.closest('[data-password-manager-login="true"]');
    const textbox = (element: EventTarget | null): element is HTMLInputElement | HTMLTextAreaElement =>
      element instanceof HTMLTextAreaElement || element instanceof HTMLInputElement && !ignoredTypes.has(element.type);
    const suppress = (element: Element) => {
      if (exempt(element)) return;
      for (const [name, value] of Object.entries({ autocomplete: 'off', 'data-bwignore': 'true', 'data-lpignore': 'true', 'data-1p-ignore': 'true', 'data-form-type': 'other' })) {
        if (element.getAttribute(name) !== value) element.setAttribute(name, value);
      }
      if (textbox(element) && !accepted.has(element)) accepted.set(element, element.value);
    };
    document.querySelectorAll(selector).forEach(suppress);
    const observer = new MutationObserver(records => {
      for (const record of records) {
        const textarea = record.type === 'characterData' ? record.target.parentElement : record.target;
        if (textarea instanceof HTMLTextAreaElement) accepted.set(textarea, textarea.value);
        if (record.type === 'attributes' && record.target instanceof Element) {
          if (record.target.matches(selector)) suppress(record.target);
          // React mirrors controlled values into the value attribute. Keep
          // legitimate account/configuration loads in sync with the baseline.
          if (record.attributeName === 'value' && textbox(record.target)) accepted.set(record.target, record.target.value);
        }
        for (const node of record.addedNodes) if (node instanceof Element) {
          if (node.matches(selector)) suppress(node);
          node.querySelectorAll(selector).forEach(suppress);
        }
      }
    });
    observer.observe(document.body, { childList: true, characterData: true, subtree: true, attributes: true,
      attributeFilter: ['value', 'autocomplete', 'data-bwignore', 'data-lpignore', 'data-1p-ignore', 'data-form-type'] });
    const guard = (event: Event) => {
      const field = event.target;
      if (!textbox(field) || exempt(field)) return;
      if (event.isTrusted) { accepted.set(field, field.value); return; }
      // Extension script events must not reach React handlers and mark a
      // credential/configuration draft dirty. Real typing and paste still work.
      field.value = accepted.get(field) ?? field.defaultValue;
      event.stopImmediatePropagation();
    };
    document.addEventListener('input', guard, true);
    document.addEventListener('change', guard, true);
    return () => {
      observer.disconnect();
      document.removeEventListener('input', guard, true);
      document.removeEventListener('change', guard, true);
    };
  }, []);
  return null;
}
