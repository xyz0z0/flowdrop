import { useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, setCsrfToken, type Item, type SessionInfo } from './api';
import { isSecureContext } from './clipboard';
import { Login } from './Login';
import { Composer } from './Composer';
import { ItemCard } from './ItemCard';
import { useEventStream, type ConnState } from './useEventStream';
import { getDeviceName, setDeviceName } from './device';

const PAGE_SIZE = 30;

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
        snapshotting.current = true;
        setLoading(true);
        try {
          const page = await api.list(undefined, PAGE_SIZE);
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
          if (err instanceof ApiError && err.status === 401) {
            setSession({ authenticated: false });
            return;
          }
          setError(err instanceof Error ? err.message : '加载失败');
        } finally {
          snapshotting.current = false;
          setLoading(false);
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
    setLoadingMore(true);
    try {
      const page = await api.list(nextCursor, PAGE_SIZE);
      setItems((prev) => {
        const seen = new Set(prev.map((it) => it.id));
        return [...prev, ...page.items.filter((it) => !seen.has(it.id))];
      });
      setNextCursor(page.next_cursor);
    } catch (err) {
      notify(err instanceof Error ? err.message : '加载更多失败', 'error');
    } finally {
      setLoadingMore(false);
    }
  };

  const onCreated = (item: Item) => {
    setItems((prev) => (prev.some((it) => it.id === item.id) ? prev : [item, ...prev]));
  };

  const onDelete = async (id: string) => {
    const snapshot = items;
    setItems((prev) => prev.filter((it) => it.id !== id));
    try {
      await api.remove(id);
      notify('已删除');
    } catch (err) {
      setItems(snapshot);
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
            <p className="sub">共享收件箱</p>
          </div>
        </div>
        <div className="topbar-right">
          <span className={`conn conn-${conn}`} title={connLabel(conn)}>
            {connLabel(conn)}
          </span>
          <button className="ghost" onClick={onLogout} type="button">
            退出
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

      <main className="inbox">
        {loading && items.length === 0 && <p className="muted center">正在加载…</p>}
        {!loading && items.length === 0 && (
          <div className="empty">
            <p className="empty-title">收件箱是空的</p>
            <p className="muted">
              在上面输入文字或链接，或把文件拖进页面。手机与电脑登录同一 Hub 即可互见。
            </p>
          </div>
        )}
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
