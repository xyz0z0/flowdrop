import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  api,
  ApiError,
  setCsrfToken,
  type CategoryFilter,
  type Item,
  type PresenceDevice,
  type SessionInfo,
} from './api';
import { isSecureContext } from './clipboard';
import { Login } from './Login';
import { Composer } from './Composer';
import { ItemCard, type DownloadStatus } from './ItemCard';
import { useEventStream, type ConnState } from './useEventStream';
import { getDeviceName, setDeviceName } from './device';

const PAGE_SIZE = 30;

/** Filter chips in display order; the Hub owns what each category contains. */
const CATEGORIES: { value: CategoryFilter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'text', label: '文字' },
  { value: 'link', label: '链接' },
  { value: 'image', label: '图片' },
  { value: 'video', label: '视频' },
  { value: 'audio', label: '音频' },
  { value: 'document', label: '文档' },
  { value: 'archive', label: '压缩包' },
  { value: 'installer', label: '安装包' },
  { value: 'other', label: '其他' },
];

function categoryLabel(filter: CategoryFilter): string {
  return CATEGORIES.find((c) => c.value === filter)?.label ?? filter;
}

function inCategory(item: Item, filter: CategoryFilter): boolean {
  return filter === 'all' || item.category === filter;
}

interface Toast {
  id: number;
  text: string;
  tone: 'ok' | 'error';
}

const TOAST_MS = 3500;

// Linux GTK3 desktop only: the native WebKit context reports the real outcome of
// an inbox item download (finished without failed). The page then mirrors that
// state instead of guessing. Read at click time, not at module load: the bridge
// script is injected by the native window and may land after the bundle starts.
function nativeDownloadReports(): boolean {
  return typeof window !== 'undefined' && window.__flowdropDesktopDownloads === true;
}

// A click marks the item as downloading before WebKit's own signal can leave the
// native side. If that signal never comes (bridge missing, a request the native
// tracker does not own), the mark is dropped after this long: an unconfirmed
// status must never lock the entry.
const DOWNLOAD_ACK_MS = 8000;

// Plain-browser fallback (no download outcome is ever reported there): a
// double-click dispatches two clicks, so a second request inside this window is
// treated as the second half of that double-click.
const DOUBLE_CLICK_MS = 1200;

declare global {
  interface Window {
    /** Set by the injected Linux GTK3 bridge (desktop/downloads_track.go). */
    __flowdropDesktopDownloads?: boolean;
  }
}

export function App() {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [booting, setBooting] = useState(true);
  const [items, setItems] = useState<Item[]>([]);
  const [nextCursor, setNextCursor] = useState('');
  const [category, setCategory] = useState<CategoryFilter>('all');
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [device, setDevice] = useState(getDeviceName());
  const [error, setError] = useState('');
  // Plain-HTTP LAN origins are not secure contexts, so clipboard/notification
  // APIs may be blocked. Say so once instead of silently failing later.
  const [showHttpNotice, setShowHttpNotice] = useState(!isSecureContext());
  // Events that arrive while a REST snapshot is in flight. The snapshot may
  // predate them, so they are re-applied after it lands (see pendingEvents).
  const snapshotting = useRef(false);
  const pendingCreates = useRef<Set<string>>(new Set());
  // The category every request is issued for. Read at request time so the
  // resync callback can stay stable across chip switches.
  const categoryRef = useRef<CategoryFilter>('all');
  // Bumped whenever the visible category changes (and by every resync). A
  // response whose token is stale belongs to a previous category and must not
  // touch items/cursor/error state anymore.
  const requestSeq = useRef(0);
  // Separate token for "load more", so a resync does not strand its spinner and
  // a category switch does not let a stale page append itself.
  const loadMoreSeq = useRef(0);
  // Online devices are their own concern: `null` means no snapshot has ever
  // been received (so the UI must not claim "0 online"), `presenceStale` means
  // a snapshot exists but is no longer confirmed. The generation counter below
  // is independent of the item request generation, so a presence response can
  // neither be cancelled by a category switch nor land on a new identity.
  const [online, setOnline] = useState<PresenceDevice[] | null>(null);
  const [presenceStale, setPresenceStale] = useState(false);
  const [presenceOpen, setPresenceOpen] = useState(false);
  const presenceSeq = useRef(0);

  // `merge` replaces a toast with the same text and tone instead of stacking a
  // duplicate (used for consecutive deletes); other toasts are left alone and
  // every toast still dismisses on its own timer.
  const notify = useCallback((text: string, tone: Toast['tone'] = 'ok', merge = false) => {
    const id = Date.now() + Math.random();
    setToasts((t) => {
      if (merge && t.some((x) => x.text === text && x.tone === tone)) return t;
      return [...t, { id, text, tone }];
    });
    // A no-op filter when this toast was merged into the existing one.
    window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), TOAST_MS);
  }, []);

  // Per-item download state. `downloadStatusRef` is the same state read at click
  // time: a double click can be dispatched before React has rendered the state
  // from the first click. Kept for this page identity (survives filter and
  // category changes, dropped on logout or a Hub switch); the list card and the
  // image preview both read it.
  const [downloadStatus, setDownloadStatus] = useState<ReadonlyMap<string, DownloadStatus>>(
    new Map(),
  );
  const downloadStatusRef = useRef(new Map<string, DownloadStatus>());
  // Timers for the optimistic "downloading" mark, keyed by item.
  const downloadTimersRef = useRef(new Map<string, number>());
  // Plain-browser fallback: last click time per item (double-click guard).
  const browserClicksRef = useRef(new Map<string, number>());

  const clearDownloadTimer = useCallback((id: string) => {
    const timer = downloadTimersRef.current.get(id);
    if (timer === undefined) return;
    window.clearTimeout(timer);
    downloadTimersRef.current.delete(id);
  }, []);

  const setDownloadState = useCallback((id: string, status: DownloadStatus | null) => {
    if (status === null) {
      if (!downloadStatusRef.current.delete(id)) return;
    } else {
      if (downloadStatusRef.current.get(id) === status) return;
      downloadStatusRef.current.set(id, status);
    }
    setDownloadStatus((prev) => {
      const next = new Map(prev);
      if (status === null) next.delete(id);
      else next.set(id, status);
      return next;
    });
  }, []);

  /** Drops every per-item download record and timer (identity change, logout). */
  const resetDownloads = useCallback(() => {
    for (const timer of downloadTimersRef.current.values()) window.clearTimeout(timer);
    downloadTimersRef.current.clear();
    browserClicksRef.current.clear();
    downloadStatusRef.current.clear();
    setDownloadStatus((prev) => (prev.size === 0 ? prev : new Map()));
  }, []);

  // Native outcomes for the desktop WebView. The event carries only {id, status}
  // — the saved path never leaves the native side.
  useEffect(() => {
    const onDownload = (event: Event) => {
      const detail = (
        event as CustomEvent<{ id?: unknown; status?: unknown; existing?: unknown }>
      ).detail;
      if (!detail || typeof detail.id !== 'string') return;
      const id = detail.id;
      // Any native event is the acknowledgement this entry was waiting for.
      clearDownloadTimer(id);
      switch (detail.status) {
        case 'downloading':
          // News only when this page did not raise the mark itself, e.g. after a
          // reload while WebKit is still writing the same item.
          if (downloadStatusRef.current.get(id) !== 'downloading') {
            notify('正在下载，请稍候');
          }
          setDownloadState(id, 'downloading');
          break;
        case 'done':
          setDownloadState(id, 'done');
          notify(detail.existing === true ? '已下载，不重复保存' : '下载已完成');
          break;
        case 'failed':
          // A failed attempt is retryable: the entry is a normal download again.
          setDownloadState(id, null);
          notify('下载失败，可重试', 'error');
          break;
      }
    };
    window.addEventListener('flowdrop:download', onDownload);
    return () => window.removeEventListener('flowdrop:download', onDownload);
  }, [clearDownloadTimer, notify, setDownloadState]);

  /**
   * Decides what one download click does. Returning false swallows the click, so
   * no second download request reaches WebKit. The desktop answers from native
   * state (which survives reloads); a plain browser keeps the old behaviour.
   */
  const onDownload = useCallback(
    (id: string): boolean => {
      if (!nativeDownloadReports()) {
        const now = Date.now();
        const last = browserClicksRef.current.get(id);
        if (last !== undefined && now - last < DOUBLE_CLICK_MS) return false;
        browserClicksRef.current.set(id, now);
        // Let the anchor's native download run first, then swap the entry: a
        // browser never reports whether the file landed, so this only says the
        // download was started and keeps an explicit retry.
        window.setTimeout(() => setDownloadState(id, 'started'), 0);
        return true;
      }
      const current = downloadStatusRef.current.get(id);
      if (current === 'downloading') {
        notify('正在下载，请稍候');
        return false;
      }
      if (current === 'done') {
        // The saved file may have been deleted since, so the click is passed to
        // WebKit: the native tracker re-checks its record and cancels the request
        // before any second file is written.
        return true;
      }
      // Optimistic mark until WebKit's own signal confirms it.
      setDownloadState(id, 'downloading');
      clearDownloadTimer(id);
      const timer = window.setTimeout(() => {
        downloadTimersRef.current.delete(id);
        if (downloadStatusRef.current.get(id) === 'downloading') {
          // No native signal: never keep an unconfirmed status.
          setDownloadState(id, null);
        }
      }, DOWNLOAD_ACK_MS);
      downloadTimersRef.current.set(id, timer);
      return true;
    },
    [clearDownloadTimer, notify, setDownloadState],
  );

  useEffect(() => {
    let alive = true;
    api
      .session()
      .then((info) => {
        // The CSRF token doubles as the double-submit header value; keep the
        // in-memory copy in sync with the cookie handed out by the server.
        if (info.csrf_token) setCsrfToken(info.csrf_token);
        if (alive) setSession(info);
      })
      .catch(() => {
        if (alive) setSession({ authenticated: false });
      })
      .finally(() => {
        if (alive) setBooting(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  const authenticated = session?.authenticated === true;

  /** Full refresh of the first page; resets pagination state. */
  const resync = useMemo(
    () =>
      async (_reason: string) => {
        if (!authenticated) return;
        const token = ++requestSeq.current;
        const wanted = categoryRef.current;
        snapshotting.current = true;
        setLoading(true);
        try {
          const page = await api.list(undefined, PAGE_SIZE, wanted);
          if (requestSeq.current !== token) return;
          const known = new Set(page.items.map((it) => it.id));
          setItems(page.items);
          setNextCursor(page.next_cursor);
          setError('');
          snapshotting.current = false;
          // Items created during the fetch may be missing from the snapshot.
          const missed = [...pendingCreates.current].filter((id) => !known.has(id));
          pendingCreates.current.clear();
          for (const id of missed) void fetchItem(id);
        } catch (err) {
          if (requestSeq.current !== token) return;
          if (err instanceof ApiError && err.status === 401) {
            setSession({ authenticated: false });
            return;
          }
          setError(err instanceof Error ? err.message : '加载失败');
        } finally {
          // A newer request owns the loading flags now; leave them alone.
          if (requestSeq.current === token) {
            snapshotting.current = false;
            setLoading(false);
          }
        }
      },
    [authenticated],
  );

  /**
   * Reads the Hub's online snapshot. Every refresh trigger goes through here;
   * a failure keeps the previous list but marks it as not current, and never
   * touches the item list or its request generation.
   */
  const refreshPresence = useMemo(
    () => async () => {
      if (!authenticated) return;
      const token = ++presenceSeq.current;
      try {
        const res = await api.presence();
        if (presenceSeq.current !== token) return;
        setOnline(Array.isArray(res.online) ? res.online : []);
        setPresenceStale(false);
      } catch (err) {
        if (presenceSeq.current !== token) return;
        if (err instanceof ApiError && err.status === 401) {
          // Same session-expiry handling as the item list.
          setSession({ authenticated: false });
          return;
        }
        setPresenceStale(true);
      }
    },
    [authenticated],
  );

  const conn: ConnState = useEventStream({
    enabled: authenticated,
    onResync: (reason) => {
      void resync(reason);
      // Independent of resync: a presence failure cannot block the item list.
      void refreshPresence();
    },
    onEvent: (type, id) => {
      if (type === 'item.created') {
        if (snapshotting.current) {
          pendingCreates.current.add(id);
          return;
        }
        // Fetch the single item so the payload is authoritative, then merge.
        void fetchItem(id);
      } else if (type === 'item.deleted') {
        pendingCreates.current.delete(id);
        setItems((prev) => prev.filter((it) => it.id !== id));
      } else if (type === 'presence.changed') {
        // Only presence is refreshed here; the frame carries no device data.
        void refreshPresence();
      }
    },
  });

  // A new identity (login, logout, Hub switch) invalidates any presence response
  // still in flight and drops the previous identity's list.
  const identity = `${session?.device_id ?? ''}|${session?.hub?.id ?? ''}`;
  useEffect(() => {
    presenceSeq.current += 1;
    setOnline(null);
    setPresenceStale(false);
    setPresenceOpen(false);
    // Download state belongs to the identity that started it.
    resetDownloads();
  }, [identity, resetDownloads]);

  // First authenticated load. Socket open/reconnect/foreground arrive through
  // onResync above, and opening the list refreshes again.
  useEffect(() => {
    if (!authenticated) return;
    void refreshPresence();
  }, [authenticated, refreshPresence]);

  // A closed socket means the snapshot can no longer be confirmed (the Hub
  // notices silent disconnects late), so it is shown as not current.
  useEffect(() => {
    if (!authenticated) return;
    if (conn === 'closed') setPresenceStale(true);
  }, [authenticated, conn]);

  const togglePresence = () => {
    const next = !presenceOpen;
    setPresenceOpen(next);
    if (next) void refreshPresence();
  };

  // Escape dismisses the list, like any other popover.
  useEffect(() => {
    if (!presenceOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setPresenceOpen(false);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [presenceOpen]);

  // No count until a snapshot has been received and is still confirmed; the
  // plain entry label replaces it instead of pretending "0".
  const presenceLabel = online !== null && !presenceStale ? `在线 ${online.length}` : '在线设备';

  async function fetchItem(id: string) {
    try {
      const res = await fetch(`/api/v1/items/${encodeURIComponent(id)}`, {
        credentials: 'same-origin',
        cache: 'no-store',
      });
      if (res.status === 404) {
        setItems((prev) => prev.filter((it) => it.id !== id));
        return;
      }
      if (!res.ok) return;
      const item = (await res.json()) as Item;
      // A created item is merged only when it belongs to the visible category;
      // the category may have changed while this single-item fetch was open.
      if (!inCategory(item, categoryRef.current)) return;
      setItems((prev) => {
        if (prev.some((it) => it.id === item.id)) return prev;
        return [item, ...prev];
      });
    } catch {
      /* the next resync will repair state */
    }
  }

  const loadMore = async () => {
    if (!nextCursor || loadingMore) return;
    const token = requestSeq.current;
    const loadToken = ++loadMoreSeq.current;
    const wanted = categoryRef.current;
    setLoadingMore(true);
    try {
      const page = await api.list(nextCursor, PAGE_SIZE, wanted);
      if (requestSeq.current !== token) return;
      setItems((prev) => {
        const seen = new Set(prev.map((it) => it.id));
        return [...prev, ...page.items.filter((it) => !seen.has(it.id))];
      });
      setNextCursor(page.next_cursor);
    } catch (err) {
      if (requestSeq.current !== token) return;
      notify(err instanceof Error ? err.message : '加载更多失败', 'error');
    } finally {
      if (loadMoreSeq.current === loadToken) setLoadingMore(false);
    }
  };

  /** Switch the visible category: drop the old page and load the new first page. */
  const changeCategory = (next: CategoryFilter) => {
    if (next === categoryRef.current) return;
    categoryRef.current = next;
    setCategory(next);
    // Invalidate every in-flight request (snapshot, loadMore, single fetch) so
    // no response for the previous category can be written into this one.
    requestSeq.current += 1;
    loadMoreSeq.current += 1;
    pendingCreates.current.clear();
    setItems([]);
    setNextCursor('');
    setLoadingMore(false);
    setError('');
    void resync('category');
  };

  const onCreated = (item: Item) => {
    if (!inCategory(item, categoryRef.current)) {
      notify(`已发送，但不在当前分类「${categoryLabel(categoryRef.current)}」下`);
      return;
    }
    setItems((prev) => (prev.some((it) => it.id === item.id) ? prev : [item, ...prev]));
  };

  const onDelete = async (id: string) => {
    const token = requestSeq.current;
    const snapshot = items;
    setItems((prev) => prev.filter((it) => it.id !== id));
    try {
      await api.remove(id);
      notify('已删除', 'ok', true);
    } catch (err) {
      // Restoring the snapshot would resurrect the previous category's list.
      if (requestSeq.current === token) setItems(snapshot);
      notify(err instanceof Error ? err.message : '删除失败', 'error');
    }
  };

  const onLogout = async () => {
    try {
      await api.logout();
    } catch {
      /* still clear local state */
    }
    setItems([]);
    setNextCursor('');
    // Online devices belong to the identity that just ended.
    presenceSeq.current += 1;
    setOnline(null);
    setPresenceStale(false);
    setPresenceOpen(false);
    // Downloads belonged to the session that is ending.
    resetDownloads();
    setSession({ authenticated: false });
  };

  if (booting) {
    return (
      <div className="boot">
        <div className="spinner" aria-hidden />
        <p>正在连接 FlowDrop…</p>
      </div>
    );
  }

  if (!authenticated) {
    return (
      <Login
        onSuccess={(info) => {
          setSession(info);
          setLoading(true);
        }}
      />
    );
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <span className="logo" aria-hidden="true">
            <svg
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.9"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <path d="M3.2 6.2h9.1l-2.1-2.1" />
              <path d="M12.8 9.8H3.7l2.1 2.1" />
            </svg>
          </span>
          <div>
            <h1>FlowDrop</h1>
            <p className="sub">{session?.hub?.name ?? '共享收件箱'}</p>
          </div>
        </div>
        <div className="topbar-right">
          <div className="device-bar">
            <label htmlFor="device">本机名称</label>
            <input
              id="device"
              value={device}
              maxLength={64}
              placeholder="例如：办公室电脑"
              onChange={(e) => {
                setDevice(e.target.value);
                setDeviceName(e.target.value);
              }}
            />
          </div>
          <div className="presence">
            <button
              type="button"
              className="presence-toggle"
              aria-expanded={presenceOpen}
              aria-controls={presenceOpen ? 'presence-panel' : undefined}
              onClick={togglePresence}
            >
              {presenceLabel}
            </button>
            {presenceOpen && (
              <div className="presence-panel" id="presence-panel" role="group" aria-label="在线设备">
                {online !== null && online.length > 0 && (
                  <ul className="presence-list">
                    {online.map((d) => (
                      <li key={d.id} className="presence-item">
                        <span className="presence-name">{d.name}</span>
                        {d.current && <span className="presence-badge">本机</span>}
                      </li>
                    ))}
                  </ul>
                )}
                {online !== null && online.length === 0 && (
                  <p className="presence-empty">暂无在线设备</p>
                )}
                {online === null && !presenceStale && (
                  <p className="presence-empty">正在获取…</p>
                )}
                {presenceStale && (
                  <p className="presence-empty">
                    状态待更新
                    <button
                      className="link"
                      type="button"
                      onClick={() => void refreshPresence()}
                    >
                      重试
                    </button>
                  </p>
                )}
              </div>
            )}
          </div>
          <span className={`conn conn-${conn}`} title={connLabel(conn)}>
            <span className="conn-dot" aria-hidden />
            {connLabel(conn)}
          </span>
          <button className="ghost" onClick={onLogout} type="button">
            解除配对
          </button>
        </div>
      </header>

      {showHttpNotice && (
        <div className="banner notice" role="status">
          <span>
            当前通过 HTTP 访问：浏览器可能禁用自动复制等能力。复制失败时，条目会展开可手动选择的文本（Ctrl/⌘+C）。
          </span>
          <button className="link" onClick={() => setShowHttpNotice(false)} type="button">
            知道了
          </button>
        </div>
      )}

      <Composer
        device={device}
        onCreated={onCreated}
        notify={notify}
        {...(session?.limits?.max_upload_bytes !== undefined
          ? { maxUploadBytes: session.limits.max_upload_bytes }
          : {})}
      />

      {error && (
        <div className="banner error" role="alert">
          {error}
          <button className="link" onClick={() => void resync('manual')} type="button">
            重试
          </button>
        </div>
      )}

      <nav className="filters" aria-label="按文件类型筛选">
        {CATEGORIES.map((c) => (
          <button
            key={c.value}
            type="button"
            className="chip"
            aria-pressed={category === c.value}
            onClick={() => changeCategory(c.value)}
          >
            {c.label}
          </button>
        ))}
      </nav>

      <main className="inbox">
        {loading && items.length === 0 && <p className="muted center">正在加载…</p>}
        {!loading && items.length === 0 && category === 'all' && (
          <div className="empty">
            <p className="empty-title">收件箱是空的</p>
            <p className="muted">
              在上面输入文字或链接，或把文件拖进页面。手机与电脑配对同一个 Hub 即可互见。
            </p>
          </div>
        )}
        {!loading && items.length === 0 && category !== 'all' && (
          <div className="empty">
            <p className="empty-title">该分类暂无内容</p>
            <p className="muted">收件箱里还没有「{categoryLabel(category)}」类型的条目。</p>
            <button className="ghost" onClick={() => changeCategory('all')} type="button">
              查看全部
            </button>
          </div>
        )}
        {items.length > 0 && (
          <div className="inbox-card">
            <ul className="item-list">
              {items.map((item) => (
                <ItemCard
                  key={item.id}
                  item={item}
                  onDelete={onDelete}
                  notify={notify}
                  downloadStatus={downloadStatus.get(item.id) ?? 'idle'}
                  onDownload={onDownload}
                />
              ))}
            </ul>
          </div>
        )}
        {nextCursor && (
          <button className="more" onClick={() => void loadMore()} disabled={loadingMore} type="button">
            {loadingMore ? '加载中…' : '加载更早的内容'}
          </button>
        )}
      </main>

      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.tone}`}>
            {t.text}
          </div>
        ))}
      </div>
    </div>
  );
}

function connLabel(c: ConnState): string {
  switch (c) {
    case 'open':
      return '实时同步中';
    case 'connecting':
      return '连接中…';
    case 'closed':
      return '已离线，重连中';
  }
}
