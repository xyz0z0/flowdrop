import { ApiError } from './api';

/** Pairing codes are exactly four decimal digits; leading zeros are valid. */
export const PAIRING_CODE_LENGTH = 4;

/**
 * Keeps only decimal digits and caps the length. Pasting "12 34" or "^1234"
 * yields "1234", and a leading zero is never stripped.
 */
export function normalizePairingCode(raw: string): string {
  return raw.replace(/[^0-9]/g, '').slice(0, PAIRING_CODE_LENGTH);
}

export function isCompletePairingCode(code: string): boolean {
  return /^[0-9]{4}$/.test(code);
}

/**
 * The Hub answers every unusable code with 401 pairing_invalid: wrong digits,
 * expired, already consumed, or no active code at all. 429 pairing_locked is
 * reserved for an exhausted attempt budget (docs/PAIRING.md). The client cannot
 * tell "wrong digits" from "expired/consumed" apart, so the invalid message
 * covers both; network failures stay a separate case.
 */
export type PairingErrorKind = 'invalid' | 'locked' | 'network' | 'other';

export function classifyPairingError(err: unknown): PairingErrorKind {
  if (err instanceof ApiError) {
    switch (err.code) {
      case 'pairing_invalid':
        return 'invalid';
      case 'pairing_locked':
        return 'locked';
      case 'network':
        return 'network';
      default:
        return err.status === 0 ? 'network' : 'other';
    }
  }
  return 'other';
}

export function pairingErrorMessage(err: unknown): string {
  switch (classifyPairingError(err)) {
    case 'invalid':
      return '配对码错误或已失效。请在电脑的 FlowDrop 设置里点「添加设备」，获取新的四位配对码。';
    case 'locked':
      return '配对码尝试次数已用完，本次配对已锁定。请在电脑的 FlowDrop 设置里重新生成配对码。';
    case 'network':
      return '无法连接 Hub，请确认电脑上的 FlowDrop 正在运行，并检查网络或 Tailscale 连接。';
    case 'other':
      return err instanceof Error ? err.message : '配对失败，请重试。';
  }
}
