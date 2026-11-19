/**
 * Clipboard writes with an explicit, honest failure path.
 *
 * A Hub reached over plain HTTP on a home LAN is usually **not** a secure
 * context, so `navigator.clipboard` may be missing and `execCommand('copy')`
 * may be refused. Copying is therefore best-effort: this module never throws,
 * only reports success when the browser confirms it, and lets the caller offer
 * a "select the text manually" fallback.
 */

export type CopyFailureReason = 'insecure-context' | 'denied' | 'unsupported';

export type CopyResult =
  | { ok: true; method: 'clipboard-api' | 'exec-command' }
  | { ok: false; reason: CopyFailureReason };

/** True when the page may use secure-context-only browser APIs. */
export function isSecureContext(): boolean {
  return typeof window !== 'undefined' && window.isSecureContext === true;
}

/** True when the async Clipboard API is even present. */
export function hasClipboardApi(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    typeof navigator.clipboard?.writeText === 'function'
  );
}

/**
 * Attempts to copy `value`, preferring the async Clipboard API and falling back
 * to the deprecated `execCommand('copy')` path. Never rejects.
 */
export async function copyText(value: string): Promise<CopyResult> {
  let reason: CopyFailureReason = 'unsupported';

  if (hasClipboardApi()) {
    if (isSecureContext()) {
      try {
        await navigator.clipboard.writeText(value);
        return { ok: true, method: 'clipboard-api' };
      } catch (err) {
        // NotAllowedError/SecurityError = permission or gesture rejected;
        // anything else is treated as unavailable.
        reason =
          err instanceof Error &&
          (err.name === 'NotAllowedError' || err.name === 'SecurityError')
            ? 'denied'
            : 'unsupported';
      }
    } else {
      // Browsers may expose a stub that always rejects on insecure origins;
      // the legacy path is still worth one attempt below.
      reason = 'insecure-context';
    }
  } else if (!isSecureContext()) {
    reason = 'insecure-context';
  }

  if (legacyCopy(value)) return { ok: true, method: 'exec-command' };
  return { ok: false, reason };
}

/**
 * Deprecated textarea + execCommand copy. Works in some non-secure contexts,
 * but the browser can silently refuse it, so its boolean is the only signal we
 * trust.
 */
function legacyCopy(value: string): boolean {
  if (typeof document === 'undefined') return false;
  if (typeof document.execCommand !== 'function') return false;

  const ta = document.createElement('textarea');
  ta.value = value;
  ta.setAttribute('readonly', '');
  ta.setAttribute('aria-hidden', 'true');
  ta.tabIndex = -1;
  ta.style.position = 'fixed';
  ta.style.top = '0';
  ta.style.left = '-9999px';
  ta.style.opacity = '0';
  ta.style.pointerEvents = 'none';

  let selected = false;
  try {
    document.body.appendChild(ta);
    ta.select();
    ta.setSelectionRange(0, value.length);
    selected = true;
  } catch {
    selected = false;
  }

  let ok = false;
  if (selected) {
    try {
      ok = document.execCommand('copy');
    } catch {
      ok = false;
    }
  }

  try {
    document.body.removeChild(ta);
  } catch {
    /* the node may already be gone */
  }
  return ok;
}

/** Short, non-misleading explanation for a failed automatic copy. */
export function copyFailureHint(reason: CopyFailureReason): string {
  switch (reason) {
    case 'insecure-context':
      return '当前页面经 HTTP 访问，浏览器未开放自动复制；请手动选择下面的文本复制。';
    case 'denied':
      return '浏览器拒绝了剪贴板权限；请手动选择下面的文本复制。';
    default:
      return '此浏览器不支持自动复制；请手动选择下面的文本复制。';
  }
}
