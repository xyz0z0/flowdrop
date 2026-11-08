import { useState, type FormEvent } from 'react';
import { api, ApiError, type SessionInfo } from './api';

interface Props {
  onSuccess: (info: SessionInfo) => void;
}

export function Login({ onSuccess }: Props) {
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notInitialized, setNotInitialized] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const info = await api.login(username.trim() || 'admin', password);
      setPassword('');
      onSuccess(info);
    } catch (err) {
      if (err instanceof ApiError && err.code === 'not_initialized') {
        setNotInitialized(true);
        setError(err.message);
      } else {
        setError(err instanceof Error ? err.message : '登录失败');
      }
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
        <p className="muted">登录你的私有共享收件箱</p>

        <label htmlFor="username">用户名</label>
        <input
          id="username"
          name="username"
          autoComplete="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          disabled={busy}
        />

        <label htmlFor="password">管理员密码</label>
        <input
          id="password"
          name="password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={busy}
          autoFocus
        />

        {error && (
          <p className="error-text" role="alert">
            {error}
          </p>
        )}

        {notInitialized && (
          <div className="hint">
            <p>在 Hub 电脑的终端执行：</p>
            <code>flowdrop admin init</code>
            <p>按提示设置管理员密码后，回到此页面登录。</p>
          </div>
        )}

        <button className="primary" type="submit" disabled={busy || password.length === 0}>
          {busy ? '登录中…' : '登录'}
        </button>
        <p className="fine muted">
          单用户私有部署。会话存储在服务器，可随时退出。家庭局域网可经 HTTP 访问；
          跨网请使用 Tailscale。HTTP 下浏览器可能禁用自动复制，届时可手动选择文本复制。
        </p>
      </form>
    </div>
  );
}
