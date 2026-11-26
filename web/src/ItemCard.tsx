import { memo, useEffect, useRef, useState, type MouseEvent, type RefObject } from 'react';
import { api, type Item } from './api';
import { copyFailureHint, copyText, type CopyFailureReason } from './clipboard';
import { formatBytes } from './Composer';
import { splitTextLinks } from './textLinks';

/**
 * Per-item download state. "downloading"/"done" come from the native desktop
 * tracker (real WebKit outcome); "started" is the plain-browser fallback, where
 * no outcome is ever reported.
 */
export type DownloadStatus = 'downloading' | 'done' | 'started';

interface Props {
  item: Item;
  onDelete: (id: string) => Promise<void> | void;
  notify: (text: string, tone?: 'ok' | 'error') => void;
  /** Shared per-item download state; the list card and the preview both read it. */
  downloadStatus: DownloadStatus | 'idle';
  /** Runs on every download click; false means this click must not download. */
  onDownload: (id: string) => boolean;
}

function DownloadIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M8 2.2v8.2" />
      <path d="m4.8 7.4 3.2 3.2 3.2-3.2" />
      <path d="M2.6 13.4h10.8" />
    </svg>
  );
}

interface DownloadActionProps {
  url: string;
  fileName: string;
  status: DownloadStatus | 'idle';
  /** "下载" / "下载原图" — the untouched native anchor's label. */
  label: string;
  /** Base classes for the primary entry (idle anchor and in-flight states). */
  primaryClassName: string;
  icon?: boolean;
  onDownload: () => boolean;
}

/**
 * Native `<a href download>` keeps the browser/WebView download and its
 * filename. The desktop WebView reports the real outcome, so the entry shows
 * "下载中…" while WebKit writes and "已下载" after a successful download; both
 * states keep answering clicks (feedback, never a second file). In a plain
 * browser the outcome is unknowable, so the entry falls back to "已发起下载"
 * with an explicit "重试下载".
 */
function DownloadAction({
  url,
  fileName,
  status,
  label,
  primaryClassName,
  icon = false,
  onDownload,
}: DownloadActionProps) {
  const click = (e: MouseEvent<HTMLAnchorElement>) => {
    // A click the page refuses (already downloading) must not reach WebKit.
    if (!onDownload()) e.preventDefault();
  };
  if (status === 'started') {
    return (
      <>
        <span className={`${primaryClassName} download-started`} aria-disabled="true">
          {icon && <DownloadIcon />}
          已发起下载
        </span>
        <a className="ghost button download-retry" href={url} download={fileName} onClick={click}>
          重试下载
        </a>
      </>
    );
  }
  if (status === 'downloading') {
    return (
      <button
        type="button"
        className={`${primaryClassName} download-pending`}
        title="正在下载，完成后会显示结果"
        onClick={() => onDownload()}
      >
        {icon && <DownloadIcon />}
        下载中…
      </button>
    );
  }
  // Idle or already saved. A saved entry stays an anchor on purpose: the native
  // tracker re-checks its record, so a deleted file can be downloaded again and
  // a repeat never writes a second copy.
  return (
    <a
      className={status === 'done' ? `${primaryClassName} download-done` : primaryClassName}
      href={url}
      download={fileName}
      title={status === 'done' ? '已下载；再点一次只确认状态，不会覆盖现有文件' : undefined}
      onClick={click}
    >
      {icon && <DownloadIcon />}
      {status === 'done' ? '已下载' : label}
    </a>
  );
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

// Rough number of characters that fit on one line of a collapsed card. Used only
// to estimate how tall a message renders, so it can be kept generous.
const LINE_CHARS = 40;
// Lines shown before the text is collapsed.
const COLLAPSED_LINES = 6;
// Stable character threshold: above this a text message is always measured.
const LONG_TEXT_CHARS = 280;

/**
 * Cheap guess at whether a text message may need folding.
 *
 * Character counts cannot know the real column width, so this is only a gate
 * that keeps short messages from being measured at all; the toggle itself is
 * decided by the measured overflow in [useFoldState]. The estimate stays
 * deliberately pessimistic (many short lines) because a false positive costs
 * one layout read, while a false negative would clip text with no way to
 * reveal it.
 */
function mayNeedExpand(raw: string): boolean {
  const text = raw.replace(/\r\n?/g, '\n').trim();
  if (!text) return false;
  if (text.length > LONG_TEXT_CHARS) return true;
  let lines = 0;
  for (const line of text.split('\n')) {
    lines += Math.max(1, Math.ceil(line.length / LINE_CHARS));
  }
  return lines > COLLAPSED_LINES;
}

/**
 * How a text body is laid out: `fold` keeps it clamped at [COLLAPSED_LINES]
 * with a toggle, `flat` shows everything, `unmeasured` is the first paint of a
 * possibly long message (clamped, so its real overflow can be measured).
 */
type FoldState = 'unmeasured' | 'fold' | 'flat';

/**
 * Measures whether clamping would hide a worthwhile amount of text.
 *
 * The rendered height depends on the column width and on how the message
 * happens to wrap, which no character estimate can predict: a 269-character
 * message with two long URLs is 7 lines in a wide desktop window but 9+ lines
 * on a phone, and the same message must fold on one and not on the other. A
 * toggle that reveals a single line of URL tail reads as broken, so a message
 * is only folded when unfolding reveals at least two lines.
 */
function useFoldState(
  ref: RefObject<HTMLParagraphElement | null>,
  longText: boolean,
  expanded: boolean,
  text: string,
): FoldState {
  const [fold, setFold] = useState<FoldState>('unmeasured');

  useEffect(() => {
    if (!longText) {
      setFold('flat');
      return;
    }
    // Only the collapsed box hides anything; measure once it is shown again.
    if (expanded) return;
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      // `scrollHeight` is the full content height whether or not the clamp is
      // active, so the same comparison works in every state and engine.
      const line = parseFloat(getComputedStyle(el).lineHeight) || 20;
      const hidden = el.scrollHeight - COLLAPSED_LINES * line;
      const next: FoldState = hidden >= line * 2 ? 'fold' : 'flat';
      // Assigning the same value re-renders nothing, so this cannot loop.
      setFold(next);
    };
    measure();
    // Window resizes change the wrap and therefore the overflow.
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, longText, expanded, text]);

  return fold;
}

export const ItemCard = memo(function ItemCard({
  item,
  onDelete,
  notify,
  downloadStatus,
  onDownload,
}: Props) {
  const [previewOpen, setPreviewOpen] = useState(false);
  // Per-item: survives re-renders and event-stream updates of the same item.
  const [textExpanded, setTextExpanded] = useState(false);
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
  const text = item.text ?? '';
  const longText = item.type === 'text' && mayNeedExpand(text);
  const textRef = useRef<HTMLParagraphElement | null>(null);
  const folded = useFoldState(textRef, longText, textExpanded, text);
  // `unmeasured` stays clamped so the first paint can be measured.
  const showClamp = longText && !textExpanded && folded !== 'flat';
  const showToggle = longText && folded === 'fold';
  // Stable target for the toggle's aria-controls.
  const textId = `item-text-${item.id}`;

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
        {showToggle && (
          <button
            type="button"
            className="ghost text-toggle"
            aria-expanded={textExpanded}
            aria-controls={textId}
            onClick={() => setTextExpanded((v) => !v)}
          >
            {textExpanded ? '收起' : '展开全文'}
          </button>
        )}
      </div>

      {item.type === 'text' && (
        <p id={textId} ref={textRef} className={showClamp ? 'text-body clamp' : 'text-body'}>
          {splitTextLinks(text).map((piece, i) =>
            piece.kind === 'url' ? (
              <a
                key={i}
                href={piece.value}
                target="_blank"
                rel="noopener noreferrer nofollow"
                className="inline-link"
              >
                {piece.value}
              </a>
            ) : (
              piece.value
            ),
          )}
        </p>
      )}

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
          <span
            className={`file-icon${/\.apk$/i.test(item.blob.name) ? ' file-icon-apk' : ''}`}
            aria-hidden="true"
          >
            {/\.apk$/i.test(item.blob.name) ? (
              <svg
                width="18"
                height="18"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M8 1.6 13.6 4.8v6.4L8 14.4 2.4 11.2V4.8L8 1.6Z" />
                <path d="M2.4 4.8 8 8l5.6-3.2" />
                <path d="M8 8v6.4" />
                <path d="M5.2 3.2 10.8 6.4" />
              </svg>
            ) : (
              <svg
                width="18"
                height="18"
                viewBox="0 0 16 16"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <path d="M9.5 1.8H4.2A1.4 1.4 0 0 0 2.8 3.2v9.6a1.4 1.4 0 0 0 1.4 1.4h7.6a1.4 1.4 0 0 0 1.4-1.4V5.5L9.5 1.8Z" />
                <path d="M9.4 1.8v3.8h3.8" />
                <path d="M5.4 8.6h5.2M5.4 11.2h3.6" />
              </svg>
            )}
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
            className="ghost action-primary"
            onClick={() => void doCopy('内容', item.text ?? '')}
          >
            <svg
              width="13"
              height="13"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <rect x="5.4" y="5.4" width="8.4" height="8.4" rx="1.6" />
              <path d="M3.4 10.6H2.8A1.4 1.4 0 0 1 1.4 9.2V2.8A1.4 1.4 0 0 1 2.8 1.4h6.4a1.4 1.4 0 0 1 1.4 1.4v.6" />
            </svg>
            {copied ? '已复制' : '复制'}
          </button>
        )}
        {isLink && item.text && (
          <a
            className="ghost button action-primary"
            href={item.text}
            target="_blank"
            rel="noopener noreferrer nofollow"
          >
            <svg
              width="13"
              height="13"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="M9.6 2.4h4v4" />
              <path d="M13.6 2.4 7.4 8.6" />
              <path d="M7.2 3.6H3.8A1.4 1.4 0 0 0 2.4 5v7.2a1.4 1.4 0 0 0 1.4 1.4H11a1.4 1.4 0 0 0 1.4-1.4V8.8" />
            </svg>
            打开链接
          </a>
        )}
        {item.blob && (
          <DownloadAction
            url={api.downloadUrl(item.id)}
            fileName={item.blob.name}
            status={downloadStatus}
            label="下载"
            primaryClassName="ghost button action-primary"
            icon
            onDownload={() => onDownload(item.id)}
          />
        )}
        {item.blob && (
          <button
            type="button"
            className="ghost action-secondary"
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
          <button
            type="button"
            className="ghost danger-text action-secondary"
            onClick={() => setConfirming(true)}
          >
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
            <DownloadAction
              url={api.downloadUrl(item.id)}
              fileName={item.blob.name}
              status={downloadStatus}
              label="下载原图"
              primaryClassName="ghost button"
              onDownload={() => onDownload(item.id)}
            />
            <button type="button" className="ghost" onClick={() => setPreviewOpen(false)}>
              关闭
            </button>
          </div>
        </div>
      )}
    </li>
  );
});
