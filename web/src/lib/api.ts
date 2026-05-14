import { getTelegramSession } from './telegram';

const API_BASE = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? 'http://127.0.0.1:8080';

export type SpikeEvent = {
  id: string;
  mint: string;
  ratio: number;
  windowVolume: number;
  baselinePer5m: number;
  uniqueWallets: number;
  createdAt: string;
};

export type ListenerWatchInput = {
  mint: string;
  symbol?: string;
  autoSnipeEnabled?: boolean;
};

export async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(API_BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {})
    }
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Request failed: ${res.status}`);
  }
  return (await res.json()) as T;
}

export async function fetchRecentSpikes(): Promise<SpikeEvent[]> {
  const data = await fetchJSON<{ items: SpikeEvent[] }>('/api/v1/spikes/recent');
  return data.items ?? [];
}

export async function fetchMyListeners(): Promise<string[]> {
  const tg = getTelegramSession();
  if (!tg.initData) {
    return [];
  }
  const data = await fetchJSON<{ mints: string[] }>('/api/v1/listeners/active', {
    headers: {
      'X-Telegram-Init-Data': tg.initData
    }
  });
  return data.mints ?? [];
}

export async function addListener(input: ListenerWatchInput): Promise<void> {
  const tg = getTelegramSession();
  if (!tg.initData) {
    throw new Error('Telegram session is required to add listeners');
  }
  await fetchJSON('/api/v1/listeners/watch', {
    method: 'POST',
    headers: {
      'X-Telegram-Init-Data': tg.initData
    },
    body: JSON.stringify({
      mint: input.mint,
      symbol: input.symbol ?? '',
      autoSnipeEnabled: !!input.autoSnipeEnabled
    })
  });
}

export async function removeListener(mint: string): Promise<void> {
  const tg = getTelegramSession();
  if (!tg.initData) {
    throw new Error('Telegram session is required to remove listeners');
  }
  await fetchJSON('/api/v1/listeners/watch', {
    method: 'DELETE',
    headers: {
      'X-Telegram-Init-Data': tg.initData
    },
    body: JSON.stringify({ mint })
  });
}

export function apiBaseURL(): string {
  return API_BASE;
}
