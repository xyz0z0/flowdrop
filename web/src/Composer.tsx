import { useCallback, useRef, useState, type DragEvent } from 'react';
import { api, uploadFile, type Item, type UploadHandle } from './api';

interface Props {
  device: string;
  onCreated: (item: Item) => void;
  notify: (text: string, tone?: 'ok' | 'error') => void;
  /** Server-reported per-file limit; falls back to the documented default. */
  maxUploadBytes?: number;
}

interface Pending {
  key: string;
  name: string;
  size: number;
  loaded: number;
  status: 'uploading' | 'failed';
  error?: string;
  handle: UploadHandle;
}

const DEFAULT_MAX_UPLOAD = 512 * 1024 * 1024;

export function Composer({ device, onCreated, notify, maxUploadBytes }: Props) {
  const maxUpload = maxUploadBytes && maxUploadBytes > 0 ? maxUploadBytes : DEFAULT_MAX_UPLOAD;
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [pending, setPending] = useState<Pending[]>([]);
  const fileInput = useRef<HTMLInputElement | null>(null);
  const dragDepth = useRef(0);

  const looksLikeLink = /^https?:\/\/\S+$/i.test(text.trim());

  const sendText = async () => {
    const value = text.trim();
    if (!value || sending) return;
    setSending(true);
    try {
      const item = await api.createText(looksLikeLink ? 'link' : 'text', value, device);
      onCreated(item);
      setText('');
    } catch (err) {
      notify(err instanceof Error ? err.message : '发送失败', 'error');
    } finally {
      setSending(false);
    }
  };

  const startUploads = useCallback(
    (files: FileList | File[]) => {
      const list = Array.from(files);
      if (list.length === 0) return;
      // 每附件一个条目：逐个顺序上传，避免打满磁盘与并发限制。
      void (async () => {
        for (const file of list) {
          if (file.size > maxUpload) {
            notify(`「${file.name}」超过 ${formatBytes(maxUpload)} 上限，已跳过`, 'error');
            continue;
          }
          const key = `${Date.now()}-${file.name}-${Math.random()}`;
          const handle = uploadFile(file, device, (loaded) => {
            setPending((prev) =>
              prev.map((p) => (p.key === key ? { ...p, loaded } : p)),
            );
          });
          setPending((prev) => [
            ...prev,
            { key, name: file.name, size: file.size, loaded: 0, status: 'uploading', handle },
          ]);
          try {
            const item = await handle.promise;
            onCreated(item);
          } catch (err) {
            const message = err instanceof Error ? err.message : '上传失败';
            if (!/取消/.test(message)) {
              setPending((prev) =>
                prev.map((p) =>
                  p.key === key ? { ...p, status: 'failed', error: message } : p,
                ),
              );
              notify(`「${file.name}」上传失败：${message}`, 'error');
            } else {
              setPending((prev) => prev.filter((p) => p.key !== key));
            }
          } finally {
            setPending((prev) => prev.filter((p) => p.key !== key || p.status === 'failed'));
          }
        }
      })();
    },
    [device, maxUpload, notify, onCreated],
  );

  const onDrop = (e: DragEvent) => {
    e.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    if (e.dataTransfer.files.length > 0) startUploads(e.dataTransfer.files);
  };

  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.files);
    if (files.length > 0) {
      e.preventDefault();
      startUploads(files);
    }
  };

  return (
    <section
      className={`composer${dragging ? ' dragging' : ''}`}
      onDragEnter={(e) => {
        if (!e.dataTransfer.types.includes('Files')) return;
        e.preventDefault();
        dragDepth.current += 1;
        setDragging(true);
      }}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes('Files')) e.preventDefault();
      }}
      onDragLeave={(e) => {
        e.preventDefault();
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (dragDepth.current === 0) setDragging(false);
      }}
      onDrop={onDrop}
    >
      <textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        onPaste={onPaste}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
            e.preventDefault();
            void sendText();
          }
        }}
        placeholder="输入或粘贴文字/链接…（Ctrl/⌘ + Enter 发送；可直接粘贴截图）"
        rows={3}
        maxLength={65536}
        disabled={sending}
      />
      <div className="composer-actions">
        <div className="left">
          <button type="button" className="ghost" onClick={() => fileInput.current?.click()}>
            选择文件
          </button>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              if (e.target.files) startUploads(e.target.files);
              e.target.value = '';
            }}
          />
          <span className="muted small">
            {looksLikeLink ? '将作为链接发送' : '支持文字、图片、APK 与普通文件'}
          </span>
        </div>
        <button
          type="button"
          className="primary"
          onClick={() => void sendText()}
          disabled={sending || text.trim().length === 0}
        >
          {sending ? '发送中…' : '发送'}
        </button>
      </div>

      {pending.length > 0 && (
        <ul className="uploads">
          {pending.map((p) => (
            <li key={p.key} className={p.status}>
              <div className="upload-row">
                <span className="upload-name" title={p.name}>
                  {p.name}
                </span>
                <span className="muted small">
                  {p.status === 'failed'
                    ? '失败'
                    : `${Math.round((p.loaded / Math.max(p.size, 1)) * 100)}% · ${formatBytes(p.loaded)} / ${formatBytes(p.size)}`}
                </span>
                <button
                  type="button"
                  className="link"
                  onClick={() => {
                    if (p.status === 'uploading') p.handle.cancel();
                    setPending((prev) => prev.filter((x) => x.key !== p.key));
                  }}
                >
                  {p.status === 'uploading' ? '取消' : '移除'}
                </button>
              </div>
              {p.status === 'failed' && p.error && <p className="error-text small">{p.error}</p>}
              <progress
                className="bar"
                value={p.status === 'failed' ? 0 : p.loaded}
                max={Math.max(p.size, 1)}
              />
            </li>
          ))}
        </ul>
      )}

      {dragging && <div className="drop-overlay">松开以上传文件</div>}
    </section>
  );
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}
