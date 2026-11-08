const KEY = 'flowdrop.device';

export function getDeviceName(): string {
  try {
    return localStorage.getItem(KEY) ?? detectDefault();
  } catch {
    return detectDefault();
  }
}

export function setDeviceName(name: string): void {
  try {
    localStorage.setItem(KEY, name);
  } catch {
    /* storage may be unavailable in private mode */
  }
}

function detectDefault(): string {
  const ua = navigator.userAgent;
  if (/Android/i.test(ua)) return 'Android';
  if (/iPhone|iPad|iPod/i.test(ua)) return 'iOS';
  if (/Windows/i.test(ua)) return 'Windows';
  if (/Mac OS X/i.test(ua)) return 'Mac';
  if (/Linux/i.test(ua)) return 'Linux';
  return '设备';
}
