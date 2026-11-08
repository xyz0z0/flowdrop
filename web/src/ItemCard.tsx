import { memo, useEffect, useRef, useState } from 'react';
import { api, type Item } from './api';
import { copyFailureHint, copyText, type CopyFailureReason } from './clipboard';
import { formatBytes } from './Composer';

interface Props {
  item: Item;
  onDelete: (id: string) => Promise<void> | void;
  notify: (text: string, tone?: 'ok' | 'error') => void;
}

function hostOf(raw: string): string {
  try {
    return new URL(raw).host;
  } catch {
    return '';
  }
}

const kindLabel: Record<string, string> = {
  text: '文字',
  link: '链接',
  image: '图片',
  file: '文件',
};

export const ItemCard = memo(function ItemCard({ item, onDelete, notify }: Props) {
  const [previewOpen, setPreviewOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const [confirming, setConfirming] = useState(false);
  // Shown only after an automatic copy failed: the value stays visible and
  // pre-selected so the user can copy it by hand on plain-HTTP LAN origins.
  const [manual, setManual] = useState<{ label: string; value: string; reason: CopyFailureReason } | null>(
    null,
  );
  const manualRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    const el = manualRef.current;
    if (!el) return;
    // Select eagerly: on insecure origins the user may only need Ctrl/⌘+C.
    el.focus();
    el.select();
    el.setSelectionRange(0, el.value.length);
  }, [manual]);

  const isImage = item.type === 'image' && item.blob;
  const isLink = item.type === 'link';

  const doCopy = async (label: string, value: string) => {
    const result = await copyText(value);
    if (result.ok) {
      setManual(null);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
      notify('已复制到剪贴板');
      return;
    }
    // Never claim success here: surface the reason and let the user copy by hand.
    setManual({ label, value, reason: result.reason });
    notify('无法自动复制，已展开文本供手动复制', 'error');
  };

  const selectAll = () => {
    const el = manualRef.current;
    if (!el) return;
    el.focus();
    el.select();
    el.setSelectionRange(0, el.value.length);
  };

  return (
    <li className={`item item-${item.type}`}>
      <div className="item-head">
        <span className={`tag tag-${item.type}`}>{kindLabel[item.type] ?? item.type}</span>
        <span className="muted small">
          {item.sender_device ? `${item.sender_device} · ` : ''}
          {new Date(item.created_at).toLocaleString()}
        </span>
      </div>

      {item.type === 'text' && <p className="text-body">{item.text}</p>}

      {isLink && item.text && (
        <div className="link-body">
          <a href={item.text} target="_blank" rel="noopener noreferrer nofollow" className="link-url">
            {hostOf(item.text) || item.text}
          </a>
          <p className="muted small break">{item.text}</p>
          <p className="fine muted">
            外部链接：FlowDrop 不校验其内容，打开前请确认来源可信。
          </p>
        </div>
      )}

      {isImage && item.blob && (
        <div className="image-body">
          <button
            type="button"
            className="thumb"
            onClick={() => setPreviewOpen(true)}
            aria-label={`预览图片 ${item.blob.name}`}
          >
            <img
              src={api.previewUrl(item.id)}
              alt={item.blob.name}
              loading="lazy"
              decoding="async"
              width={item.blob.size_bytes > 2_000_000 ? 320 : undefined}
            />
          </button>
          <p className="muted small">
            {item.blob.name} · {formatBytes(item.blob.size_bytes)}
          </p>
        </div>
      )}

      {item.type === 'file' && item.blob && (
        <div className="file-body">
          <span className="file-icon" aria-hidden>
            {/\.apk$/i.test(item.blob.name) ? '📦' : '📄'}
          </span>
          <div className="file-meta">
            <span className="break">{item.blob.name}</span>
            <span className="muted small">
              {formatBytes(item.blob.size_bytes)}
              {/\.apk$/i.test(item.blob.name) ? ' · Android 安装包' : ''}
            </span>
          </div>
        </div>
      )}

      <div className="item-actions">
        {(item.type === 'text' || isLink) && (
          <button
            type="button"
            className="ghost"
            onClick={() => void doCopy('内容', item.text ?? '')}
          >
            {copied ? '已复制' : '复制'}
          </button>
        )}
        {isLink && item.text && (
          <a className="ghost button" href={item.text} target="_blank" rel="noopener noreferrer nofollow">
            打开链接
          </a>
        )}
        {item.blob && (
          <a className="ghost button" href={api.downloadUrl(item.id)} download={item.blob.name}>
            下载
          </a>
        )}
        {item.blob && (
          <button
            type="button"
            className="ghost"
            title={item.blob.sha256}
            onClick={() => void doCopy('SHA-256', item.blob?.sha256 ?? '')}
          >
            复制 SHA-256
          </button>
        )}
        {confirming ? (
          <span className="confirm">
            <button
              type="button"
              className="danger"
              onClick={() => {
                setConfirming(false);
                void onDelete(item.id);
              }}
            >
              确认删除
            </button>
            <button type="button" className="ghost" onClick={() => setConfirming(false)}>
              取消
            </button>
          </span>
        ) : (
          <button type="button" className="ghost danger-text" onClick={() => setConfirming(true)}>
            删除
          </button>
        )}
      </div>

      {manual && (
        <div className="manual-copy" role="group" aria-label="手动复制">
          <p className="fine muted">{copyFailureHint(manual.reason)}</p>
          <textarea
            ref={manualRef}
            readOnly
            value={manual.value}
            rows={Math.min(6, Math.max(2, Math.ceil(manual.value.length / 60)))}
            aria-label={`${manual.label}（可手动复制）`}
            onFocus={(e) => e.currentTarget.select()}
          />
          <div className="manual-copy-actions">
            <button type="button" className="ghost" onClick={selectAll}>
              全选
            </button>
            <button type="button" className="ghost" onClick={() => setManual(null)}>
              收起
            </button>
          </div>
        </div>
      )}

      {isImage && item.blob && previewOpen && (
        <div
          className="lightbox"
          role="dialog"
          aria-modal="true"
          aria-label="图片预览"
          onClick={() => setPreviewOpen(false)}
        >
          <img src={api.previewUrl(item.id)} alt={item.blob.name} />
          <div className="lightbox-bar" onClick={(e) => e.stopPropagation()}>
            <span className="muted small">{item.blob.name}</span>
            <a className="ghost button" href={api.downloadUrl(item.id)} download={item.blob.name}>
              下载原图
            </a>
            <button type="button" className="ghost" onClick={() => setPreviewOpen(false)}>
              关闭
            </button>
          </div>
        </div>
      )}
    </li>
  );
});
