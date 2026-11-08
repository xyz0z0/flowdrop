export type ItemType = 'text' | 'link' | 'image' | 'file';

export interface Blob {
  id: string;
  name: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  created_at: number;
}

export interface Item {
  id: string;
  type: ItemType;
  text?: string;
  sender_device: string;
  created_at: number;
  blob?: Blob;
}

/** How an endpoint address reaches the Hub; only reported, never inferred. */
export type HubEndpointKind = 'lan' | 'tailscale' | 'other';

export interface HubEndpointInfo {
  url: string;
  kind: HubEndpointKind;
}

/** Stable Hub identity plus its advertised addresses (see docs/PAIRING.md). */
export interface HubInfo {
  id: string;
  name: string;
  endpoints: HubEndpointInfo[];
}

export interface SessionInfo {
  authenticated: boolean;
  csrf_token?: string;
  expires_at?: number;
  /** Present once a device is paired. */
  device_id?: string;
  hub?: HubInfo;
  /** Server-declared limits so the UI does not hardcode the default. */
  limits?: { max_upload_bytes: number };
}

export interface Page {
  items: Item[];
  next_cursor: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

let csrfToken = '';

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

export function getCsrfToken(): string {
  if (csrfToken) return csrfToken;
  const match = document.cookie.match(/(?:^|;\s*)flowdrop_csrf=([^;]+)/);
  csrfToken = match?.[1] ? decodeURIComponent(match[1]) : '';
  return csrfToken;
}

async function parseError(res: Response): Promise<ApiError> {
  let code = 'http_' + res.status;
  let message = `请求失败（HTTP ${res.status}）`;
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string } };
    if (body.error?.code) code = body.error.code;
    if (body.error?.message) message = body.error.message;
  } catch {
    /* non-JSON error body */
  }
  return new ApiError(res.status, code, message);
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  const method = (init.method ?? 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    const token = getCsrfToken();
    if (token) headers.set('X-FlowDrop-CSRF', token);
  }
  let res: Response;
  try {
    res = await fetch(path, {
      ...init,
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
    });
  } catch (err) {
    throw new ApiError(0, 'network', '无法连接 Hub，请检查网络：' + String(err));
  }
  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export const api = {
  session: () => request<SessionInfo>('/api/v1/auth/session'),

  /**
   * Redeems a four-digit pairing code for this browser. The Web client never
   * asks for a native device token: the session lives only in HttpOnly
   * cookies, so nothing is written to localStorage/sessionStorage here.
   */
  pair: async (code: string, deviceName: string): Promise<SessionInfo> => {
    const info = await request<SessionInfo>('/api/v1/auth/pair', {
      method: 'POST',
      body: JSON.stringify({ code, device_name: deviceName }),
    });
    if (info.csrf_token) setCsrfToken(info.csrf_token);
    return info;
  },

  logout: async (): Promise<void> => {
    await request<void>('/api/v1/auth/logout', { method: 'POST' });
    setCsrfToken('');
  },

  list: (cursor?: string, limit = 30): Promise<Page> => {
    const q = new URLSearchParams({ limit: String(limit) });
    if (cursor) q.set('cursor', cursor);
    return request<Page>(`/api/v1/items?${q.toString()}`);
  },

  createText: (type: 'text' | 'link', text: string, senderDevice: string): Promise<Item> =>
    request<Item>('/api/v1/items/text', {
      method: 'POST',
      body: JSON.stringify({ type, text, sender_device: senderDevice }),
    }),

  remove: (id: string): Promise<void> =>
    request<void>(`/api/v1/items/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  downloadUrl: (id: string): string => `/api/v1/items/${encodeURIComponent(id)}/download`,
  previewUrl: (id: string): string => `/api/v1/items/${encodeURIComponent(id)}/preview`,
};

export interface UploadHandle {
  promise: Promise<Item>;
  cancel: () => void;
}

/**
 * Uploads exactly one file. XHR is used because fetch still has no portable
 * upload progress event. Progress is indeterminate for compressed bodies, so
 * we display what the browser reports and always finish on the response.
 */
export function uploadFile(
  file: File,
  senderDevice: string,
  onProgress: (loaded: number, total: number) => void,
): UploadHandle {
  const xhr = new XMLHttpRequest();
  let settled = false;

  const promise = new Promise<Item>((resolve, reject) => {
    const form = new FormData();
    if (senderDevice) form.append('sender_device', senderDevice);
    form.append('file', file, file.name);

    xhr.open('POST', '/api/v1/items/upload', true);
    xhr.withCredentials = true;
    const token = getCsrfToken();
    if (token) xhr.setRequestHeader('X-FlowDrop-CSRF', token);
    xhr.responseType = 'json';

    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable) onProgress(ev.loaded, ev.total);
    };
    xhr.onload = () => {
      settled = true;
      const body = xhr.response as
        | { error?: { code?: string; message?: string } }
        | Item
        | null;
      if (xhr.status >= 200 && xhr.status < 300 && body && 'id' in body) {
        onProgress(file.size, file.size);
        resolve(body as Item);
        return;
      }
      const err = body && 'error' in body ? body.error : undefined;
      reject(
        new ApiError(
          xhr.status,
          err?.code ?? 'upload_failed',
          err?.message ?? `上传失败（HTTP ${xhr.status}）`,
        ),
      );
    };
    xhr.onerror = () => {
      settled = true;
      reject(new ApiError(0, 'network', '上传中断：无法连接 Hub'));
    };
    xhr.onabort = () => {
      settled = true;
      reject(new ApiError(0, 'aborted', '已取消上传'));
    };
    xhr.send(form);
  });

  return {
    promise,
    cancel: () => {
      if (!settled) xhr.abort();
    },
  };
}
