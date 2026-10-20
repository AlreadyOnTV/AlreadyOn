// Each reusable filter dimension has an independent card and header action.
import { useEffect, useRef, useState } from 'react';
import {
  moveFilterOption,
  restoreFilterOrder,
  getFilterOptions,
  saveFilterOption,
  deleteFilterOption,
  getCertifications,
  type FilterCatalog,
  type FilterOption,
} from '../../api/client';
import SettingToggle from './SettingToggle';
import InfoModal from './InfoModal';
import { PencilIcon, TrashIcon } from './AdminIcons';

export default function ConfigureFilters({ onChanged }: { onChanged: () => void }) {
  const [catalog, setCatalog] = useState<FilterCatalog>({
    age: [],
    era: [],
  });
  const [ratings, setRatings] = useState<string[]>([]);
  const [editing, setEditing] = useState<{
    kind: 'age' | 'era';
    option: FilterOption;
  } | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [years, setYears] = useState({ start: '', end: '' });
  const dragStart = useRef<{
    x: number;
    y: number;
    active: boolean;
    row: HTMLTableRowElement;
    targetId?: string;
  } | null>(null);
  const longPressTimer = useRef<number | null>(null);
  const [dragged, setDragged] = useState<{ kind: string; id: string } | null>(null);
  const [dropTarget, setDropTarget] = useState<{ kind: string; id: string; edge: 'before' | 'after' } | null>(null);
  const clearLongPress = () => {
    if (longPressTimer.current !== null) window.clearTimeout(longPressTimer.current);
    longPressTimer.current = null;
  };
  useEffect(() => () => clearLongPress(), []);
  // React's delegated context-menu callback can lose a timing race with the
  // desktop menu Chrome synthesizes while DevTools touch emulation is active.
  // A native capture listener cancels that browser default before delegation.
  useEffect(() => {
    const suppressRowMenu = (event: MouseEvent) => {
      if (!(event.target instanceof Element) || !event.target.closest('.filter-definition-table .filter-draggable-row')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
    };
    document.addEventListener('contextmenu', suppressRowMenu, true);
    return () => document.removeEventListener('contextmenu', suppressRowMenu, true);
  }, []);
  // Once a touch long-press activates reordering, keep the page from scrolling
  // under the user's finger until that drag finishes.
  useEffect(() => {
    if (!dragged) return;
    const holdPage = (event: TouchEvent) => event.preventDefault();
    document.addEventListener('touchmove', holdPage, { passive: false });
    return () => document.removeEventListener('touchmove', holdPage);
  }, [dragged]);
  const move = async (kind: 'age' | 'era', id: string, direction: 'up' | 'down' | 'target', target?: string) => {
    setBusy(true);
    setError('');
    try {
      await moveFilterOption(kind, id, direction, target);
      await load();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      setDragged(null);
    }
  };

  const restore = async (kind: 'age' | 'era') => {
    setBusy(true);
    setError('');
    try {
      await restoreFilterOrder(kind);
      await load();
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const orderChanged = (kind: 'age' | 'era') =>
    catalog[kind].some((option, index, options) => index > 0 && (options[index - 1].defaultPosition ?? 0) > (option.defaultPosition ?? 0));
  const load = () => getFilterOptions().then(setCatalog);
  useEffect(() => {
    Promise.all([
      load(),
      getCertifications().then((c) =>
        setRatings(
          ['TV-Y', 'G', 'TV-G', 'TV-Y7', 'TV-Y7-FV', 'PG', 'TV-PG', 'PG-13', 'TV-14', 'R', 'TV-MA', 'NC-17', 'NR'].filter((r) =>
            [...c.movie, ...c.tv].includes(r),
          ),
        ),
      ),
    ]).catch((e: Error) => setError(e.message));
  }, []);
  const edit = (kind: 'age' | 'era', option?: FilterOption) => {
    setError('');
    setYears({ start: option?.startYear ? String(option.startYear) : '', end: option?.endYear ? String(option.endYear) : '' });
    if (kind === 'age' && option?.unrestricted) option = { ...option, unrestricted: false, allowUnknown: true, ratings: [...ratings] };
    setEditing({
      kind,
      option: option ?? {
        id: Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16)).join('-'),
        name: '',
        ratings: [],
      },
    });
  };
  const save = async () => {
    if (!editing) return;
    setBusy(true);
    setError('');
    try {
      const year = (value: string) => {
        const text = value.trim();
        if (!text || text === '*' || text.toLowerCase() === 'no limit') return 0;
        if (!/^\d{1,4}$/.test(text) || Number(text) < 1) throw new Error('Use a year from 1 to 9999, * or No Limit.');
        return Number(text);
      };
      await saveFilterOption(
        editing.kind,
        editing.kind === 'era' ? { ...editing.option, startYear: year(years.start), endYear: year(years.end) } : editing.option,
      );
      await load();
      setEditing(null);
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const change = (patch: Partial<FilterOption>) => setEditing((e) => (e ? { ...e, option: { ...e.option, ...patch } } : e));
  return (
    <>
      {(['age', 'era'] as const).map((kind) => (
        <div key={kind} className="admin-card filter-definition-group">
          <div className="folder-card-header">
          <h3>
            {kind === 'age' ? 'Age Restrictions' : 'Date Range'}
            {orderChanged(kind) && (
              <button type="button" className="restore-order-link" disabled={busy} onClick={() => void restore(kind)}>
                (Restore Default)
              </button>
            )}
          </h3>
          <button className="admin-btn" disabled={busy} onClick={() => edit(kind)}>
            + Add {kind === 'age' ? 'Age Restriction' : 'Date Range'}
          </button>
          </div>
          <table className="admin-table filter-definition-table">
            <thead>
              <tr>
                <th>Actions</th>
                <th>Name</th>
                {kind === 'era' && <th>Years</th>}
                <th>Display Order</th>
              </tr>
            </thead>
            <tbody>
              {catalog[kind].map((o, optionIndex) => (
                <tr
                  key={o.id}
                  data-filter-kind={kind}
                  data-filter-id={o.id}
                  data-filter-index={optionIndex}
                  className={
                    'filter-draggable-row' +
                    (dragged?.kind === kind && dragged.id === o.id ? ' is-dragging' : '') +
                    (dropTarget?.kind === kind && dropTarget.id === o.id ? ` is-drop-${dropTarget.edge}` : '')
                  }
                  onPointerDown={(e) => {
                    if (busy || e.button !== 0 || (e.target as Element).closest('button')) return;
                    // Cancelling touch pointer-down suppresses Chrome's
                    // compatibility mouse/context-menu gesture. Vertical
                    // scrolling remains available through touch-action: pan-y.
                    if (e.pointerType === 'touch') e.preventDefault();
                    clearLongPress();
                    const start = { x: e.clientX, y: e.clientY, active: false, row: e.currentTarget };
                    dragStart.current = start;
                    e.currentTarget.setPointerCapture(e.pointerId);
                    if (e.pointerType === 'touch') {
                      longPressTimer.current = window.setTimeout(() => {
                        if (dragStart.current !== start) return;
                        start.active = true;
                        start.row.style.setProperty('--drag-y', '0px');
                        setDragged({ kind, id: o.id });
                      }, 500);
                    }
                  }}
                  onPointerMove={(e) => {
                    const start = dragStart.current;
                    if (!start || !e.currentTarget.hasPointerCapture(e.pointerId)) return;
                    const distance = Math.hypot(e.clientX - start.x, e.clientY - start.y);
                    if (e.pointerType === 'touch' && !start.active && distance >= 10) {
                      clearLongPress();
                      dragStart.current = null;
                      e.currentTarget.releasePointerCapture(e.pointerId);
                      return;
                    }
                    if (e.pointerType !== 'touch' && !start.active && distance >= 5) {
                      start.active = true;
                      setDragged({ kind, id: o.id });
                    }
                    if (!start.active) return;
                    if (e.pointerType === 'touch') e.preventDefault();
                    start.row.style.setProperty('--drag-y', `${e.clientY - start.y}px`);
                    const target = document.elementFromPoint(e.clientX, e.clientY)?.closest<HTMLTableRowElement>('tr[data-filter-id]');
                    if (target?.dataset.filterKind === kind && target.dataset.filterId && target.dataset.filterId !== o.id) {
                      start.targetId = target.dataset.filterId;
                      const edge = Number(target.dataset.filterIndex) < optionIndex ? 'before' : 'after';
                      setDropTarget((current) =>
                        current?.kind === kind && current.id === target.dataset.filterId && current.edge === edge
                          ? current
                          : { kind, id: target.dataset.filterId!, edge },
                      );
                    } else {
                      start.targetId = undefined;
                      setDropTarget(null);
                    }
                  }}
                  onPointerUp={(e) => {
                    clearLongPress();
                    const start = dragStart.current;
                    dragStart.current = null;
                    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
                    start?.row.style.removeProperty('--drag-y');
                    setDragged(null);
                    setDropTarget(null);
                    if (!start?.active || busy) return;
                    if (start.targetId) void move(kind, o.id, 'target', start.targetId);
                  }}
                  onPointerCancel={() => {
                    clearLongPress();
                    dragStart.current?.row.style.removeProperty('--drag-y');
                    dragStart.current = null;
                    setDragged(null);
                    setDropTarget(null);
                  }}
                  onLostPointerCapture={() => {
                    clearLongPress();
                    dragStart.current?.row.style.removeProperty('--drag-y');
                    dragStart.current = null;
                    setDragged(null);
                    setDropTarget(null);
                  }}
                  // Chrome's device emulator and touch browsers synthesize a
                  // contextmenu event at almost the same threshold as our
                  // long-press activation. Reorderable rows never need a
                  // context menu, so suppress it without a state timing race.
                  onContextMenu={(e) => e.preventDefault()}
                >
                  <td className="filter-definition-actions">
                    <button className="admin-icon-btn" aria-label={`Edit ${o.name}`} title="Edit filter" onClick={() => edit(kind, o)}>
                      <PencilIcon />
                    </button>
                    <button
                      className="admin-icon-btn"
                      aria-label={`Delete ${o.name}`}
                      title="Delete filter"
                      disabled={busy}
                      onClick={async () => {
                        setBusy(true);
                        setError('');
                        try {
                          await deleteFilterOption(kind, o.id);
                          await load();
                          onChanged();
                        } catch (e) {
                          setError((e as Error).message);
                        } finally {
                          setBusy(false);
                        }
                      }}
                    >
                      <TrashIcon />
                    </button>
                  </td>
                  <td>{o.name}</td>
                  {kind === 'era' && (
                    <td>
                      {o.startYear || '*'} – {o.endYear || '*'}
                    </td>
                  )}
                  <td>
                    <span
                      className="drag-grip"
                      tabIndex={busy ? -1 : 0}
                      role="button"
                      aria-label={'Reorder ' + o.name}
                      title="Drag to reorder; on touch, long press the row first. Arrow Up and Arrow Down also work"
                      onKeyDown={(e) => {
                        if (!busy && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) {
                          e.preventDefault();
                          void move(kind, o.id, e.key === 'ArrowUp' ? 'up' : 'down');
                        }
                      }}
                    >
                      <svg
                        aria-hidden="true"
                        width="18"
                        height="24"
                        viewBox="0 0 18 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      >
                        <path d="m4 8 5-5 5 5M4 16l5 5 5-5" />
                      </svg>
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

        </div>
      ))}
      {error && !editing && <p role="alert">{error}</p>}
      {editing && (
        <InfoModal
          open
          className={'filter-editor filter-editor-' + editing.kind}
          title={'Configure ' + (editing.kind === 'age' ? 'Age Restriction' : 'Date Range')}
          onClose={() => {
            if (!busy) setEditing(null);
          }}
        >
          <fieldset disabled={busy} style={{ border: 0, padding: 0 }}>
            <div className="admin-field-row">
              <label htmlFor="filter-name">Name</label>
              <input id="filter-name" type="text" autoFocus maxLength={100} value={editing.option.name} onChange={(e) => change({ name: e.target.value })} />
            </div>
            {editing.kind === 'age' && (
              <>
                <div className="rating-selection-actions" aria-label="Rating selection">
                  <button type="button" className="text-action" onClick={() => change({ ratings: [...ratings], allowUnknown: true, unrestricted: false })}>
                    Select All
                  </button>
                  <span aria-hidden="true"> · </span>
                  <button type="button" className="text-action" onClick={() => change({ ratings: [], allowUnknown: false, unrestricted: false })}>
                    Deselect All
                  </button>
                </div>
                <div className="modal-table-scroll">
                <table className="admin-table rating-toggle-table">
                  <thead>
                    <tr>
                      <th>Enabled</th>
                      <th>Rating</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ratings.map((rating) => (
                      <tr key={rating}>
                        <td>
                          <SettingToggle
                            label={rating}
                            checked={(editing.option.ratings ?? []).includes(rating)}
                            onChange={(enabled) =>
                              change({
                                ratings: enabled ? [...(editing.option.ratings ?? []), rating] : (editing.option.ratings ?? []).filter((r) => r !== rating),
                              })
                            }
                          />
                        </td>
                        <td>{rating}</td>
                      </tr>
                    ))}
                    <tr>
                      <td>
                        <SettingToggle label="Unknown Ratings" checked={!!editing.option.allowUnknown} onChange={(allowUnknown) => change({ allowUnknown })} />
                      </td>
                      <td>Unknown Ratings</td>
                    </tr>
                  </tbody>
                </table>
                </div>
              </>
            )}
            {editing.kind === 'era' && (
              <>
                <div className="admin-field-row">
                  <label htmlFor="era-start">Start Year</label>
                  <input
                    id="era-start"
                    type="text"
                    placeholder="No Limit"
                    value={years.start}
                    onChange={(e) => setYears({ ...years, start: e.target.value })}
                  />
                </div>
                <div className="admin-field-row">
                  <label htmlFor="era-end">End Year</label>
                  <input id="era-end" type="text" placeholder="No Limit" value={years.end} onChange={(e) => setYears({ ...years, end: e.target.value })} />
                </div>
              </>
            )}
            {error && (
              <p role="alert" className="profile-field-error">
                {error}
              </p>
            )}
            <div className="admin-table-actions">
              <button className="admin-btn" onClick={() => void save()}>
                {busy ? 'Saving…' : 'Save'}
              </button>
              <button className="admin-btn admin-btn-secondary" onClick={() => setEditing(null)}>
                Cancel
              </button>
            </div>
          </fieldset>
        </InfoModal>
      )}
    </>
  );
}
