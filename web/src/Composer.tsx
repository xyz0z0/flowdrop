import { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { api, uploadFile, type Item, type UploadHandle } from './api';
import {
  DESKTOP_DRAG_EVENT,
  DESKTOP_DROP_READY_ATTR,
  DESKTOP_FILES_EVENT,
  hasFilePayload,
  planDesktopFiles,
  planDrop,
  type DropPlan,
} from './fileDrop';

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
  // 整个内容窗口都是投放区：拖拽状态只由窗口级监听维护。
  const [dragging, setDragging] = useState(false);
  const [pending, setPending] = useState<Pending[]>([]);
  const fileInput = useRef<HTMLInputElement | null>(null);

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

  // 保存最新的 notify/startUploads，窗口监听只在挂载时安装一次，
  // 避免父组件重渲染导致拖拽途中反复解绑、丢失 dragging 状态。
  // 目录名只作中文提示，不上传、不递归；DOM 拖拽与原生桥共用。
  const applyPlan = (plan: DropPlan) => {
    if (plan.directories.length > 0) {
      const names = plan.directories.map((name) => `「${name}」`).join('、');
      notify(
        plan.files.length > 0
          ? `暂不支持拖入文件夹：${names}，已跳过，其余文件继续上传`
          : `暂不支持拖入文件夹：${names}`,
        'error',
      );
    }
    if (plan.files.length > 0) startUploads(plan.files);
  };

  const dropFiles = useRef<(dataTransfer: DataTransfer | null) => void>(() => {});
  dropFiles.current = (dataTransfer) => {
    applyPlan(planDrop(dataTransfer));
  };

  // 原生桥已给出真实 File[]：直接交同一 startUploads，认证/限制/进度/取消全沿用。
  const desktopFiles = useRef<(detail: unknown) => void>(() => {});
  desktopFiles.current = (detail) => {
    setDragging(false);
    applyPlan(planDesktopFiles(detail));
  };

  useEffect(() => {
    // Composer 仅在已登录收件箱挂载，监听随之安装/卸载。
    let depth = 0;
    const reset = () => {
      depth = 0;
      setDragging(false);
    };
    const onDragEnter = (e: globalThis.DragEvent) => {
      if (!hasFilePayload(e.dataTransfer)) return; // 保留文字/链接/选区拖拽
      e.preventDefault();
      depth += 1;
      setDragging(true);
    };
    const onDragOver = (e: globalThis.DragEvent) => {
      if (!hasFilePayload(e.dataTransfer)) return;
      // 阻止浏览器把文件当导航打开。
      e.preventDefault();
      if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
    };
    const onDragLeave = (e: globalThis.DragEvent) => {
      if (!hasFilePayload(e.dataTransfer)) return;
      // relatedTarget 为空说明已离开窗口，直接清空而不是等计数归零。
      if (!e.relatedTarget) {
        reset();
        return;
      }
      depth = Math.max(0, depth - 1);
      if (depth === 0) setDragging(false);
    };
    const onDrop = (e: globalThis.DragEvent) => {
      if (!hasFilePayload(e.dataTransfer)) return;
      // 只在窗口级处理一次，drop 不再冒泡到别处重复上传。
      e.preventDefault();
      reset();
      dropFiles.current(e.dataTransfer);
    };
    const onDragEnd = () => reset();

    window.addEventListener('dragenter', onDragEnter);
    window.addEventListener('dragover', onDragOver);
    window.addEventListener('dragleave', onDragLeave);
    window.addEventListener('drop', onDrop);
    window.addEventListener('dragend', onDragEnd);
    return () => {
      window.removeEventListener('dragenter', onDragEnter);
      window.removeEventListener('dragover', onDragOver);
      window.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('drop', onDrop);
      window.removeEventListener('dragend', onDragEnd);
      depth = 0;
      setDragging(false);
    };
  }, []);

  // 原生（desktop GTK）拖放通道：仅在已登录 Composer 挂载期间开放接收门禁与监听。
  useEffect(() => {
    const root = document.documentElement;
    root.setAttribute(DESKTOP_DROP_READY_ATTR, 'true');
    const onDesktopFiles = (e: Event) => {
      desktopFiles.current((e as CustomEvent<unknown>).detail);
    };
    const onDesktopDrag = (e: Event) => {
      // 只切换已有 overlay，不接受路径/文件，也不制造 DOM DragEvent。
      const detail = (e as CustomEvent<{ active?: unknown } | null>).detail;
      setDragging(detail?.active === true);
    };
    window.addEventListener(DESKTOP_FILES_EVENT, onDesktopFiles);
    window.addEventListener(DESKTOP_DRAG_EVENT, onDesktopDrag);
    return () => {
      window.removeEventListener(DESKTOP_FILES_EVENT, onDesktopFiles);
      window.removeEventListener(DESKTOP_DRAG_EVENT, onDesktopDrag);
      root.removeAttribute(DESKTOP_DROP_READY_ATTR);
      setDragging(false);
    };
  }, []);

  const onPaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData.files);
    if (files.length > 0) {
      e.preventDefault();
      startUploads(files);
    }
  };

  return (
    <section className={`composer${dragging ? ' dragging' : ''}`}>
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

      {dragging &&
        createPortal(
          <div className="window-drop-overlay" role="status">
            <div className="window-drop-card">
              <strong>松开鼠标即可上传</strong>
              <span className="muted small">整个窗口都可投放，支持多文件；文件夹不会被上传</span>
            </div>
          </div>,
          document.body,
        )}
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
