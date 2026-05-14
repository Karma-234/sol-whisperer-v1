const INIT_DATA_FALLBACK = (import.meta.env.VITE_TELEGRAM_INIT_DATA as string | undefined) ?? '';

export type TelegramSession = {
  initData: string;
  source: 'telegram-webapp' | 'env-fallback' | 'missing';
};

export function getTelegramSession(): TelegramSession {
  const fromWebApp = window.Telegram?.WebApp?.initData?.trim() ?? '';
  if (fromWebApp) {
    return { initData: fromWebApp, source: 'telegram-webapp' };
  }

  const fromEnv = INIT_DATA_FALLBACK.trim();
  if (fromEnv) {
    return { initData: fromEnv, source: 'env-fallback' };
  }

  return { initData: '', source: 'missing' };
}

export function ensureTelegramReady(): void {
  window.Telegram?.WebApp?.ready?.();
}
