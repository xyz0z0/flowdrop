import { useCallback, useEffect, useRef, useState } from 'react';

export type ConnState = 'connecting' | 'open' | 'closed';

interface Options {
  /** Called after every (re)connection so the page can resync via REST. */
  onResync: (reason: 'open' | 'reconnect' | 'visible' | 'online') => void;
  onEvent: (type: string, id: string) => void;
  enabled: boolean;
}

/**
 * Keeps an authenticated WebSocket open for change notifications only.
 * Reconnects with exponential backoff, and forces a REST resync whenever the
 * socket reopens or the page returns to the foreground. The server pushes no
 * state of its own; every event is a hint to re-read or adjust local state.
 */
export function useEventStream({ onResync, onEvent, enabled }: Options): ConnState {
  const [state, setState] = useState<ConnState>('closed');
  const attempt = useRef(0);
  const ws = useRef<WebSocket | null>(null);
  const timer = useRef<number | null>(null);
  const everConnected = useRef(false);
  const resyncRef = useRef(onResync);
  const eventRef = useRef(onEvent);
  const lastResyncAt = useRef(0);

  resyncRef.current = onResync;
  eventRef.current = onEvent;

  const requestResync = useCallback((reason: 'open' | 'reconnect' | 'visible' | 'online') => {
    // Guard against visibilitychange + focus firing twice in a row.
    const now = Date.now();
    if (reason === 'visible' && now - lastResyncAt.current < 1000) return;
    lastResyncAt.current = now;
    resyncRef.current(reason);
  }, []);

  useEffect(() => {
    if (!enabled) {
      setState('closed');
      return;
    }
    let disposed = false;

    const connect = () => {
      if (disposed) return;
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const socket = new WebSocket(`${proto}//${location.host}/api/v1/events`);
      ws.current = socket;
      setState('connecting');

      socket.onopen = () => {
        if (disposed) return;
        attempt.current = 0;
        setState('open');
        requestResync(everConnected.current ? 'reconnect' : 'open');
        everConnected.current = true;
      };

      socket.onmessage = (ev) => {
        try {
          const msg = JSON.parse(String(ev.data)) as { type?: string; id?: string };
          if (msg.type === 'item.created' || msg.type === 'item.deleted') {
            eventRef.current(msg.type, msg.id ?? '');
          } else if (msg.type === 'hello') {
            // Socket is live; the open handler already asked for a resync.
          }
        } catch {
          /* ignore malformed frames */
        }
      };

      const scheduleReconnect = () => {
        if (disposed) return;
        setState('closed');
        const n = Math.min(attempt.current + 1, 6);
        attempt.current = n;
        const delay = Math.min(500 * 2 ** (n - 1), 15000);
        timer.current = window.setTimeout(connect, delay);
      };

      socket.onclose = scheduleReconnect;
      socket.onerror = () => {
        socket.close();
      };
    };

    connect();

    const onVisible = () => {
      if (document.visibilityState !== 'visible') return;
      requestResync('visible');
      // A background-suspended socket often looks "open" but is stale.
      if (!ws.current || ws.current.readyState === WebSocket.CLOSED) {
        if (timer.current) window.clearTimeout(timer.current);
        attempt.current = 0;
        connect();
      }
    };
    const onOnline = () => {
      requestResync('online');
      if (!ws.current || ws.current.readyState !== WebSocket.OPEN) {
        if (timer.current) window.clearTimeout(timer.current);
        attempt.current = 0;
        connect();
      }
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('online', onOnline);
    window.addEventListener('focus', onVisible);

    return () => {
      disposed = true;
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('online', onOnline);
      window.removeEventListener('focus', onVisible);
      if (timer.current) window.clearTimeout(timer.current);
      if (ws.current) {
        ws.current.onclose = null;
        ws.current.close();
      }
    };
  }, [enabled, requestResync]);

  return state;
}
