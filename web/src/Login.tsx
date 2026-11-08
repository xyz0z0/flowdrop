import { useRef, useState, type FormEvent } from 'react';
import { api, type SessionInfo } from './api';
import { getDeviceName } from './device';
import {
  isCompletePairingCode,
  normalizePairingCode,
  pairingErrorMessage,
  PAIRING_CODE_LENGTH,
} from './pairing';

interface Props {
  onSuccess: (info: SessionInfo) => void;
}

export function Login({ onSuccess }: Props) {
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const codeRef = useRef<HTMLInputElement>(null);
  // Same browser name the inbox uses as sender_device; it is a label, not a
  // credential, so it may live in localStorage.
  const deviceName = getDeviceName();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy || !isCompletePairingCode(code)) return;
    const attempt = code;
    // The code is single-use server-side; drop it from the field and memory
    // before awaiting so a stale value can never be resubmitted or persisted.
    setCode('');
    setBusy(true);
    setError('');
    try {
      const info = await api.pair(attempt, deviceName);
      onSuccess(info);
    } catch (err) {
      setError(pairingErrorMessage(err));
      codeRef.current?.focus();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={submit}>
        <div className="login-brand">
          <span className="logo" aria-hidden>
            ⇄
          </span>
          <h1>FlowDrop</h1>
        </div>
        <p className="muted">输入电脑上的四位配对码，连上你的私有共享收件箱</p>

        <label htmlFor="pairing-code">配对码</label>
        <input
          id="pairing-code"
          name="pairing-code"
          ref={codeRef}
          type="text"
          inputMode="numeric"
          pattern="[0-9]*"
          autoComplete="one-time-code"
          maxLength={PAIRING_CODE_LENGTH}
          placeholder="0000"
          value={code}
          onChange={(e) => setCode(normalizePairingCode(e.target.value))}
          disabled={busy}
          autoFocus
          aria-describedby="pairing-hint"
        />

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}

        <button className="primary" type="submit" disabled={busy || !isCompletePairingCode(code)}>
          {busy ? '配对中…' : '配对'}
        </button>

        <div className="hint" id="pairing-hint">
          <p>在电脑上打开 FlowDrop 设置，点「添加设备」即可看到四位配对码，120 秒内有效。</p>
          <p>本设备将以「{deviceName}」的名字被记住。</p>
        </div>

        <p className="fine muted">
          配对后浏览器只在 Cookie 里保存会话，不把凭据写入本地存储；随时可以解除配对。
          HTTP 下浏览器可能禁用自动复制，届时可手动选择文本复制。
        </p>
      </form>
    </div>
  );
}
