// A dropdown whose panel holds a checkbox per option, for picking several
// values (for example genres) without a tall always-open list.
import { useEffect, useRef, useState } from "react";

export interface MultiSelectOption {
  value: string;
  label: string;
}

export default function MultiSelectDropdown({
  options,
  selected,
  onChange,
  onOpenChange,
  inlinePanel = false,
  placeholder,
  label,
}: {
  options: MultiSelectOption[];
  selected: string[];
  onChange: (next: string[]) => void;
  onOpenChange?: (open: boolean) => void;
  inlinePanel?: boolean;
  // Shown when nothing is selected.
  placeholder: string;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClickOutside = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false);
        onOpenChange?.(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        onOpenChange?.(false);
      }
    };
    window.addEventListener("click", onClickOutside);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("click", onClickOutside);
      window.removeEventListener("keydown", onKey);
    };
  }, [open, onOpenChange]);

  const toggle = (value: string) => {
    onChange(
      selected.includes(value)
        ? selected.filter((v) => v !== value)
        : [...selected, value],
    );
  };

  const summary =
    selected.length === 0
      ? placeholder
      : selected.length <= 2
        ? selected
            .map((v) => options.find((o) => o.value === v)?.label ?? v)
            .join(", ")
        : `${selected.length} selected`;

  return (
    <div className="multi-select" ref={rootRef}>
      <button
        type="button"
        className="multi-select-button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label}
        onClick={() =>
          setOpen((current) => {
            const next = !current;
            onOpenChange?.(next);
            return next;
          })
        }
      >
        <span className="multi-select-summary">{summary}</span>
        <span aria-hidden="true">▾</span>
      </button>
      {open && (
        <div
          className={
            "multi-select-panel" +
            (inlinePanel ? " multi-select-panel-inline" : "")
          }
          role="listbox"
          aria-multiselectable="true"
          aria-label={label}
        >
          {options.map((o) => (
            <label key={o.value} className="multi-select-option">
              <input
                type="checkbox"
                checked={selected.includes(o.value)}
                onChange={() => toggle(o.value)}
              />
              {o.label}
            </label>
          ))}
          {selected.length > 0 && (
            <button
              type="button"
              className="multi-select-clear"
              onClick={() => onChange([])}
            >
              Clear
            </button>
          )}
        </div>
      )}
    </div>
  );
}
