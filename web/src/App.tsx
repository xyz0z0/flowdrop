import { useEffect, useMemo, useRef, useState } from 'react';
import {
  api,
  ApiError,
  setCsrfToken,
  type CategoryFilter,
  type Item,
  type SessionInfo,
} from './api';
import { isSecureContext } from './clipboard';
import { Login } from './Login';
import { Composer } from './Composer';
import { ItemCard } from './ItemCard';
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

  const notify = (text: string, tone: Toast['tone'] = 'ok') => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, text, tone }]);
    window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 3500);
  };

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

  const conn: ConnState = useEventStream({
    enabled: authenticated,
    onResync: (reason) => {
      void resync(reason);
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
      }
    },
  });

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
      notify('已删除');
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
          <span className="logo" aria-hidden>
            ⇄
          </span>
          <div>
            <h1>FlowDrop</h1>
            <p className="sub">{session?.hub?.name ?? '共享收件箱'}</p>
          </div>
        </div>
        <div className="topbar-right">
          <span className={`conn conn-${conn}`} title={connLabel(conn)}>
            <span className="conn-dot" aria-hidden />
            {connLabel(conn)}
          </span>
          <button className="ghost" onClick={onLogout} type="button">
            解除配对
          </button>
        </div>
      </header>

      <section className="device-bar">
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
      </section>

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
