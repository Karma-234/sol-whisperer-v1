import { getTelegramSession } from "./telegram";

const DEFAULT_API_BASE = "http://127.0.0.1:8080";

function resolveApiBase(): string {
  const configured = import.meta.env.VITE_API_BASE_URL as string | undefined;
  if (configured !== undefined) {
    return configured;
  }

  if (typeof window !== "undefined") {
    return window.location.origin;
  }

  return DEFAULT_API_BASE;
}

export type SpikeEvent = {
  id: string;
  mint: string;
  name?: string;
  symbol?: string;
  ratio: number;
  windowVolume: number;
  baselinePer5m: number;
  marketCapSOL?: number;
  uniqueWallets: number;
  tokenCreatedAt?: string;
  tokenAgeSeconds?: number;
  floorConfidence?: number;
  entryScore?: number;
  entryGrade?: string;
  createdAt: string;
};

export type ListenerWatchInput = {
  mint: string;
  symbol?: string;
  autoSnipeEnabled?: boolean;
};

function authHeadersOrNull(): Record<string, string> | null {
  const tg = getTelegramSession();
  if (tg.initData) {
    return { "X-Telegram-Init-Data": tg.initData };
  }
  return null;
}

export async function fetchJSON<T>(
  path: string,
  init?: RequestInit,
): Promise<T> {
  const res = await fetch(resolveApiBase() + path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || `Request failed: ${res.status}`);
  }
  return (await res.json()) as T;
}

export async function fetchRecentSpikes(): Promise<SpikeEvent[]> {
  const data = await fetchJSON<{ items: SpikeEvent[] }>(
    "/api/v1/spikes/recent",
  );
  return data.items ?? [];
}

export async function fetchMyListeners(): Promise<string[]> {
  const authHeaders = authHeadersOrNull();
  if (!authHeaders) {
    return [];
  }
  const data = await fetchJSON<{ mints: string[] }>(
    "/api/v1/listeners/active",
    {
      headers: authHeaders,
    },
  );
  return data.mints ?? [];
}

export async function addListener(input: ListenerWatchInput): Promise<void> {
  const authHeaders = authHeadersOrNull();
  if (!authHeaders) {
    throw new Error("Telegram session is required to add listeners");
  }
  await fetchJSON("/api/v1/listeners/watch", {
    method: "POST",
    headers: authHeaders,
    body: JSON.stringify({
      mint: input.mint,
      symbol: input.symbol ?? "",
      autoSnipeEnabled: !!input.autoSnipeEnabled,
    }),
  });
}

export async function removeListener(mint: string): Promise<void> {
  const authHeaders = authHeadersOrNull();
  if (!authHeaders) {
    throw new Error("Telegram session is required to remove listeners");
  }
  await fetchJSON("/api/v1/listeners/watch", {
    method: "DELETE",
    headers: authHeaders,
    body: JSON.stringify({ mint }),
  });
}

export function apiBaseURL(): string {
  return resolveApiBase();
}
