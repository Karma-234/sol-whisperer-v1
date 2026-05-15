import { apiBaseURL } from "./api";
import { getTelegramSession } from "./telegram";

export type LiveEvent = {
  type?: string;
  stream?: "created" | "migrated";
  mint?: string;
  name?: string;
  symbol?: string;
  uri?: string;
  pool?: string;
  isMayhemMode?: boolean;
  txType?: string;
  signature?: string;
  ratio?: number;
  buyVolumeSOL?: number;
  buyCount?: number;
  uniqueWallets?: number;
  windowVolumeSOL?: number;
  baselinePer5mSOL?: number;
  marketCapSOL?: number;
  initialBuySOL?: number;
  dexId?: string;
  pairAddress?: string;
  priceUsd?: number;
  priceNative?: number;
  marketCapUsd?: number;
  liquidityUsd?: number;
  fdv?: number;
  volume5mUsd?: number;
  volume1hUsd?: number;
  buys5m?: number;
  sells5m?: number;
  pairCreatedAt?: string;
  imageUrl?: string;
  websiteUrl?: string;
  socialHandle?: string;
  tokenCreatedAt?: string;
  tokenAgeSeconds?: number;
  floorConfidence?: number;
  entryGrade?: string;
  tier?: string;
  rpcEndpoint?: string;
  priority?: "P1" | "P2" | "P3" | "P4";
  detectedAt?: string;
  rawPayload?: string;
};

export type SocketStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "closed"
  | "error";

export type LiveSocketController = {
  close: () => void;
};

type OpenSocketOptions = {
  onEvent: (evt: LiveEvent) => void;
  onError: (msg: string) => void;
  onStatus: (status: SocketStatus) => void;
};

export function openLiveSocket(
  options: OpenSocketOptions,
): LiveSocketController | null {
  const tg = getTelegramSession();

  const apiBase = apiBaseURL();
  const base = apiBase
    ? apiBase.replace(/^http/, "ws")
    : `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}`;
  const initData = tg.initData;
  let socket: WebSocket | null = null;
  let stopped = false;
  let reconnectAttempt = 0;
  let reconnectTimer: number | null = null;

  const connect = (isReconnect: boolean) => {
    if (stopped) {
      return;
    }

    options.onStatus(isReconnect ? "reconnecting" : "connecting");
    const useAuthStream = !!initData;
    const url = new URL(useAuthStream ? "/ws/stream" : "/ws/public", base);
    if (initData) {
      url.searchParams.set("tgInitData", initData);
    }

    socket = new WebSocket(url.toString());
    socket.onopen = () => {
      reconnectAttempt = 0;
      options.onStatus("connected");
    };

    socket.onmessage = (ev) => {
      try {
        const data = JSON.parse(String(ev.data)) as LiveEvent;
        options.onEvent(data);
      } catch {
        options.onError("Received non-JSON websocket message.");
      }
    };

    socket.onerror = () => {
      options.onStatus("error");
      options.onError("Websocket error.");
    };

    socket.onclose = () => {
      if (stopped) {
        options.onStatus("closed");
        return;
      }

      reconnectAttempt += 1;
      const backoffMs = Math.min(1000 * 2 ** reconnectAttempt, 10000);
      options.onStatus("reconnecting");
      reconnectTimer = window.setTimeout(() => connect(true), backoffMs);
    };
  };

  connect(false);

  return {
    close: () => {
      stopped = true;
      if (reconnectTimer != null) {
        window.clearTimeout(reconnectTimer);
      }
      socket?.close();
      options.onStatus("closed");
    },
  };
}
