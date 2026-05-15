import { useEffect, useMemo, useState, useRef } from 'react';
import { addListener, fetchMyListeners, fetchPumpPortalRecent, fetchPumpPortalWatchStats, fetchRecentSpikes, removeListener, type PumpPortalEvent, type PumpPortalWatchStats } from '../lib/api';
import { ensureTelegramReady, getTelegramSession } from '../lib/telegram';
import { openLiveSocket, type SocketStatus } from '../lib/ws';

type Priority = 'P1' | 'P2' | 'P3' | 'P4';

type FeedItem = {
  id: string;
  priority: Priority;
  source: 'live' | 'personal' | 'history';
  mint: string;
  name?: string;
  symbol?: string;
  ratio?: number;
  uniqueWallets?: number;
  windowVolume?: number;
  baselinePer5m?: number;
  portalBuyVolumeSOL?: number;
  portalBuyCount?: number;
  marketCap?: number;
  tokenCreatedAt?: string;
  tokenAgeSeconds?: number;
  floorConfidence?: number;
  entryGrade?: string;
  detectedAt?: string;
  tier?: string;
  sourceLabel?: string;
  rpcEndpoint?: string;
  rawPayload: string;
};

type StreamView = 'signals' | 'created' | 'migrated';
type LeaderDock = 'float' | 'left' | 'center' | 'right';
type SignalVolumeSource = 'desk' | 'portal';
type PortalVisibility = 'all' | 'clean';
type CornerDock = 'top-left' | 'top-right' | 'bottom-left' | 'bottom-right';
type NetworkStrength = 'good' | 'warn' | 'bad';

type PortalFeedItem = {
  id: string;
  stream: 'created' | 'migrated';
  mint: string;
  name?: string;
  symbol?: string;
  uri?: string;
  pool?: string;
  isMayhemMode?: boolean;
  txType?: string;
  signature?: string;
  marketCap?: number;
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
  detectedAt?: string;
  rawPayload: string;
};

const MAX_FEED_ITEMS = 32;
const tabs = ['Signals', 'Listeners', 'Risk', 'Settings'] as const;
const streamTabs: Array<{ id: StreamView; label: string }> = [
  { id: 'signals', label: 'Signal Tape' },
  { id: 'created', label: 'Newly Created' },
  { id: 'migrated', label: 'Newly Migrated' },
];

function shortMint(mint: string): string {
  if (mint.length <= 14) return mint;
  return `${mint.slice(0, 6)}...${mint.slice(-6)}`;
}

function formatNumber(value?: number, digits = 2): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '--';
  return value.toLocaleString(undefined, {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  });
}

function formatCompactUSD(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value) || value < 0) return '--';
  return new Intl.NumberFormat('en-US', {
    notation: 'compact',
    maximumFractionDigits: value >= 100 ? 1 : 2,
    currency: 'USD',
    style: 'currency',
  }).format(value);
}

function formatCompactRatio(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value) || !Number.isFinite(value) || value < 0) {
    return '--';
  }
  if (value >= 1_000_000_000) {
    return '1B+x';
  }
  if (value >= 10_000) {
    return `${new Intl.NumberFormat('en-US', {
      notation: 'compact',
      maximumFractionDigits: 1,
    }).format(value)}x`;
  }
  return `${new Intl.NumberFormat('en-US', {
    maximumFractionDigits: value >= 100 ? 1 : 2,
    minimumFractionDigits: 0,
  }).format(value)}x`;
}

function formatTime(value?: string): string {
  if (!value) return '--';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--';
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function formatAge(seconds?: number): string {
  if (typeof seconds !== 'number' || Number.isNaN(seconds) || seconds < 0) return '--';
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ${minutes % 60}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

function formatGrade(grade?: string): string {
  return grade === 'A' || grade === 'B' || grade === 'C' ? grade : '--';
}

function formatFloorConfidence(confidence?: number): string {
  if (typeof confidence !== 'number' || Number.isNaN(confidence) || confidence < 0) return '--';
  const pct = Math.round(confidence * 100);
  return `${pct}%`;
}

function tokenTitle(item: { name?: string; symbol?: string; mint: string }): string {
  if (item.name && item.symbol) return `${item.name} / ${item.symbol}`;
  if (item.name) return item.name;
  if (item.symbol) return item.symbol;
  return shortMint(item.mint);
}

function formatCompactSOL(value?: number): string {
  if (typeof value !== 'number' || Number.isNaN(value) || !Number.isFinite(value) || value < 0) {
    return '--';
  }
  return `${new Intl.NumberFormat('en-US', {
    notation: value >= 1000 ? 'compact' : 'standard',
    maximumFractionDigits: value >= 10 ? 2 : 4,
  }).format(value)} SOL`;
}

function formatPairAge(value?: string): string {
  if (!value) return '--';
  const created = new Date(value);
  if (Number.isNaN(created.getTime())) return '--';
  const seconds = Math.max(0, Math.floor((Date.now() - created.getTime()) / 1000));
  return formatAge(seconds);
}

function priorityLabel(priority: Priority): string {
  switch (priority) {
    case 'P1':
      return 'Critical';
    case 'P2':
      return 'Watched';
    case 'P4':
      return 'System';
    default:
      return 'Market';
  }
}

function priorityBrief(priority: Priority): string {
  switch (priority) {
    case 'P1':
      return 'Watched mint with direct urgency';
    case 'P2':
      return 'Elevated route quality';
    case 'P4':
      return 'System or low-signal event';
    default:
      return 'Broad market flow';
  }
}

export function App() {
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>('Signals');
  const [activeStreamView, setActiveStreamView] = useState<StreamView>('signals');
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    const stored = localStorage.getItem('solWhispererTheme');
    return stored === 'dark' ? 'dark' : 'light';
  });
  const [feed, setFeed] = useState<FeedItem[]>([]);
  const [signalVolumeSource, setSignalVolumeSource] = useState<SignalVolumeSource>('desk');
  const [createdFeed, setCreatedFeed] = useState<PortalFeedItem[]>([]);
  const [migratedFeed, setMigratedFeed] = useState<PortalFeedItem[]>([]);
  const [portalVisibility, setPortalVisibility] = useState<PortalVisibility>(() => {
    const stored = localStorage.getItem('pumpPortalMayhemVisibility');
    return stored === 'clean' ? 'clean' : 'all';
  });
  const [listeners, setListeners] = useState<string[]>([]);
  const [portalWatchStats, setPortalWatchStats] = useState<PumpPortalWatchStats | null>(null);
  const [mintInput, setMintInput] = useState('');
  const [authWarning, setAuthWarning] = useState('');
  const [statusMessage, setStatusMessage] = useState('');
  const [socketStatus, setSocketStatus] = useState<SocketStatus>('connecting');
  const [lastMessageAt, setLastMessageAt] = useState<string>('');
  const [solUsdRate, setSolUsdRate] = useState<number | null>(null);
  const [leaderDock, setLeaderDock] = useState<LeaderDock>(() => {
    const stored = localStorage.getItem('signalLeadersDockTarget');
    return stored === 'left' || stored === 'center' || stored === 'right' || stored === 'float' ? stored : 'float';
  });
  const [dockPos, setDockPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const [fps, setFps] = useState(0);
  const [networkRttMs, setNetworkRttMs] = useState<number | null>(null);
  const [networkLabel, setNetworkLabel] = useState('checking');
  const [networkStrength, setNetworkStrength] = useState<NetworkStrength>('warn');
  const [latencyCorner, setLatencyCorner] = useState<CornerDock>(() => {
    const stored = localStorage.getItem('latencyIndicatorCorner');
    return stored === 'top-left' || stored === 'top-right' || stored === 'bottom-left' || stored === 'bottom-right'
      ? stored
      : 'bottom-left';
  });
  const [isLatencyDragging, setIsLatencyDragging] = useState(false);
  const [activeRowIndex, setActiveRowIndex] = useState(0);
  const dragRef = useRef<{ startX: number; startY: number; offsetX: number; offsetY: number }>({ startX: 0, startY: 0, offsetX: 0, offsetY: 0 });
  const dockPosRef = useRef<{ x: number; y: number }>({ x: 0, y: 0 });
  const floatingLeaderRef = useRef<HTMLElement | null>(null);

  const session = useMemo(() => getTelegramSession(), []);
  const isAuthed = !!session.initData;

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('solWhispererTheme', theme);
  }, [theme]);

  useEffect(() => {
    localStorage.setItem('signalLeadersDockTarget', leaderDock);
    if (leaderDock !== 'float') {
      setIsDragging(false);
    }
  }, [leaderDock]);

  useEffect(() => {
    localStorage.setItem('latencyIndicatorCorner', latencyCorner);
  }, [latencyCorner]);

  useEffect(() => {
    localStorage.setItem('pumpPortalMayhemVisibility', portalVisibility);
  }, [portalVisibility]);

  // Load dock position from localStorage on mount
  useEffect(() => {
    const stored = localStorage.getItem('signalLeadersDockPos');
    if (stored) {
      try {
        const parsed = JSON.parse(stored) as { x?: number; y?: number };
        const next = { x: parsed.x ?? 0, y: parsed.y ?? 0 };
        setDockPos(next);
        dockPosRef.current = next;
      } catch {
        // ignore parse errors
      }
    }
  }, []);

  useEffect(() => {
    dockPosRef.current = dockPos;
  }, [dockPos]);

  // Handle dock drag
  const handleDockMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if (leaderDock !== 'float') return;
    if ((e.target as HTMLElement).closest('button, input, a')) return; // Don't drag when clicking buttons
    setIsDragging(true);
    dragRef.current = {
      startX: e.clientX,
      startY: e.clientY,
      offsetX: dockPos.x,
      offsetY: dockPos.y,
    };
  };

  const handleLatencyMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('button, input, a')) return;
    e.preventDefault();
    setIsLatencyDragging(true);
  };

  useEffect(() => {
    if (!isDragging) return;

    const updateDockPosition = (next: { x: number; y: number }) => {
      dockPosRef.current = next;
      setDockPos(next);
    };

    const resolveLeaderDockTarget = (rect: DOMRect): LeaderDock => {
      if (window.innerWidth < 1100) {
        return 'float';
      }

      const centerX = rect.left + rect.width / 2;
      const targets: Array<{ dock: LeaderDock; centerX: number; distance: number }> = [];
      const leftRail = document.querySelector('.left-rail');
      const centerStage = document.querySelector('.center-stage');
      const rightRail = document.querySelector('.right-rail');

      if (leftRail instanceof HTMLElement) {
        const bounds = leftRail.getBoundingClientRect();
        targets.push({ dock: 'left', centerX: bounds.left + bounds.width / 2, distance: 0 });
      }
      if (centerStage instanceof HTMLElement) {
        const bounds = centerStage.getBoundingClientRect();
        targets.push({ dock: 'center', centerX: bounds.left + bounds.width / 2, distance: 0 });
      }
      if (rightRail instanceof HTMLElement) {
        const bounds = rightRail.getBoundingClientRect();
        targets.push({ dock: 'right', centerX: bounds.left + bounds.width / 2, distance: 0 });
      }

      if (targets.length === 0) {
        return 'float';
      }

      for (const target of targets) {
        target.distance = Math.abs(centerX - target.centerX);
      }

      const nearest = targets.sort((a, b) => a.distance - b.distance)[0];
      const threshold = Math.min(220, Math.max(110, rect.width * 0.6));
      if (nearest && nearest.distance <= threshold) {
        return nearest.dock;
      }

      return 'float';
    };

    const handleMouseMove = (e: MouseEvent) => {
      const deltaX = e.clientX - dragRef.current.startX;
      const deltaY = e.clientY - dragRef.current.startY;
      const newX = dragRef.current.offsetX + deltaX;
      const newY = dragRef.current.offsetY + deltaY;
      updateDockPosition({ x: newX, y: newY });
    };

    const handleMouseUp = () => {
      setIsDragging(false);

      const rect = floatingLeaderRef.current?.getBoundingClientRect();
      const nextDock = rect ? resolveLeaderDockTarget(rect) : 'float';
      if (nextDock !== 'float') {
        setLeaderDock(nextDock);
        return;
      }

      localStorage.setItem('signalLeadersDockPos', JSON.stringify(dockPosRef.current));
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isDragging]);

  useEffect(() => {
    if (!isLatencyDragging) return;

    const resolveCorner = (x: number, y: number): CornerDock => {
      const options: Array<{ corner: CornerDock; distance: number }> = [
        { corner: 'top-left', distance: x * x + y * y },
        { corner: 'top-right', distance: (window.innerWidth - x) ** 2 + y * y },
        { corner: 'bottom-left', distance: x * x + (window.innerHeight - y) ** 2 },
        { corner: 'bottom-right', distance: (window.innerWidth - x) ** 2 + (window.innerHeight - y) ** 2 },
      ];

      return options.sort((a, b) => a.distance - b.distance)[0]?.corner ?? 'bottom-left';
    };

    const handleMouseMove = (event: MouseEvent) => {
      setLatencyCorner(resolveCorner(event.clientX, event.clientY));
    };

    const handleMouseUp = () => {
      setIsLatencyDragging(false);
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isLatencyDragging]);

  useEffect(() => {
    let frameCount = 0;
    let lastFrameAt = performance.now();
    let rafId = 0;

    const tick = (now: number) => {
      frameCount += 1;
      const elapsed = now - lastFrameAt;
      if (elapsed >= 500) {
        setFps(Math.round((frameCount * 1000) / elapsed));
        frameCount = 0;
        lastFrameAt = now;
      }
      rafId = window.requestAnimationFrame(tick);
    };

    rafId = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(rafId);
  }, []);

  useEffect(() => {
    let cancelled = false;

    const getConnection = () => {
      const nav = navigator as Navigator & {
        connection?: { effectiveType?: string; downlink?: number } & EventTarget;
        mozConnection?: { effectiveType?: string; downlink?: number } & EventTarget;
        webkitConnection?: { effectiveType?: string; downlink?: number } & EventTarget;
      };
      return nav.connection ?? nav.mozConnection ?? nav.webkitConnection;
    };

    const updateLinkLabel = () => {
      const connection = getConnection();
      if (!connection) {
        if (!cancelled) {
          setNetworkLabel(navigator.onLine ? 'browser link unknown' : 'offline');
        }
        return;
      }
      const effectiveType = connection.effectiveType ? connection.effectiveType.toUpperCase() : '';
      const downlink = typeof connection.downlink === 'number' && Number.isFinite(connection.downlink)
        ? `${connection.downlink.toFixed(connection.downlink >= 10 ? 0 : 1)} Mbps`
        : '';
      const nextLabel = [effectiveType, downlink].filter(Boolean).join(' · ');
      if (!cancelled) {
        setNetworkLabel(nextLabel || 'browser link unknown');
      }
    };

    const measureRtt = async () => {
      const startedAt = performance.now();
      try {
        const response = await fetch('/readyz', { cache: 'no-store' });
        if (!response.ok) {
          throw new Error(`readyz ${response.status}`);
        }
        const elapsed = Math.round(performance.now() - startedAt);
        if (cancelled) {
          return;
        }
        setNetworkRttMs(elapsed);
        updateLinkLabel();
        const connection = getConnection();
        const downlink = typeof connection?.downlink === 'number' ? connection.downlink : 0;
        if (elapsed <= 120 && downlink >= 5) {
          setNetworkStrength('good');
        } else if (elapsed <= 280 && downlink >= 1.5) {
          setNetworkStrength('warn');
        } else {
          setNetworkStrength('bad');
        }
      } catch {
        if (!cancelled) {
          setNetworkRttMs(null);
          setNetworkLabel(navigator.onLine ? 'degraded' : 'offline');
          setNetworkStrength('bad');
        }
      }
    };

    const handleNetworkChange = () => {
      updateLinkLabel();
      void measureRtt();
    };

    updateLinkLabel();
    void measureRtt();

    const refresh = window.setInterval(() => {
      void measureRtt();
    }, 15000);
    window.addEventListener('online', handleNetworkChange);
    window.addEventListener('offline', handleNetworkChange);
    const connection = getConnection();
    connection?.addEventListener?.('change', handleNetworkChange as EventListener);

    return () => {
      cancelled = true;
      window.clearInterval(refresh);
      window.removeEventListener('online', handleNetworkChange);
      window.removeEventListener('offline', handleNetworkChange);
      connection?.removeEventListener?.('change', handleNetworkChange as EventListener);
    };
  }, []);

  useEffect(() => {
    ensureTelegramReady();
    if (!session.initData) {
      setAuthWarning('Open Sol Whisperer from the Telegram bot menu to unlock listeners, personal spikes, and notification tests.');
    }

    fetchRecentSpikes()
      .then((items) => {
        const mapped = items.map((item) => ({
          id: item.id,
          priority: 'P3' as Priority,
          source: 'history' as const,
          mint: item.mint,
          name: item.name,
          symbol: item.symbol,
          ratio: item.ratio,
          uniqueWallets: item.uniqueWallets,
          windowVolume: item.windowVolume,
          baselinePer5m: item.baselinePer5m,
          portalBuyVolumeSOL: undefined,
          portalBuyCount: undefined,
          marketCap: item.marketCapSOL,
          tokenCreatedAt: item.tokenCreatedAt,
          tokenAgeSeconds: item.tokenAgeSeconds,
          floorConfidence: item.floorConfidence,
          entryGrade: item.entryGrade,
          detectedAt: item.createdAt,
          rawPayload: JSON.stringify(item, null, 2),
        }));
        setFeed(mapped.slice(0, MAX_FEED_ITEMS));
      })
      .catch((err: Error) => {
        setStatusMessage(`Spike history unavailable: ${err.message}`);
      });

    fetchMyListeners()
      .then(setListeners)
      .catch((err: Error) => {
        setStatusMessage(`Listeners unavailable: ${err.message}`);
      });

    fetchPumpPortalWatchStats()
      .then(setPortalWatchStats)
      .catch((err: Error) => {
        setStatusMessage(`PumpPortal watch stats unavailable: ${err.message}`);
      });

    Promise.all([
      fetchPumpPortalRecent('created'),
      fetchPumpPortalRecent('migrated'),
    ]).then(([created, migrated]) => {
      setCreatedFeed(created.map(mapPortalEvent).slice(0, MAX_FEED_ITEMS));
      setMigratedFeed(migrated.map(mapPortalEvent).slice(0, MAX_FEED_ITEMS));
    }).catch((err: Error) => {
      setStatusMessage(`PumpPortal feed unavailable: ${err.message}`);
    });

    const ws = openLiveSocket({
      onEvent: (evt) => {
        if (evt.type === 'volume_spike' || evt.type === 'personal_listener_spike') {
          setLastMessageAt(new Date().toISOString());
          const priority = (evt.priority ?? 'P3') as Priority;
          const source: FeedItem['source'] = evt.type === 'personal_listener_spike' ? 'personal' : 'live';

          setFeed((prev) => [{
            id: crypto.randomUUID(),
            priority,
            source,
            mint: evt.mint ?? 'unknown',
            name: evt.name,
            symbol: evt.symbol,
            ratio: evt.ratio,
            uniqueWallets: evt.uniqueWallets,
            windowVolume: evt.windowVolumeSOL,
            baselinePer5m: evt.baselinePer5mSOL,
            portalBuyVolumeSOL: undefined,
            portalBuyCount: undefined,
            marketCap: evt.marketCapSOL,
            tokenCreatedAt: evt.tokenCreatedAt,
            tokenAgeSeconds: evt.tokenAgeSeconds,
            floorConfidence: evt.floorConfidence,
            entryGrade: evt.entryGrade,
            detectedAt: evt.detectedAt,
            tier: evt.tier,
            sourceLabel: evt.type === 'personal_listener_spike' ? 'PumpPortal Tier A watched' : undefined,
            rpcEndpoint: evt.rpcEndpoint,
            rawPayload: JSON.stringify(evt, null, 2),
          }, ...prev].slice(0, MAX_FEED_ITEMS));
          return;
        }

        if (evt.type === 'portal_new_token' || evt.type === 'portal_migration') {
          const nextItem = mapPortalEvent({
            stream: evt.stream ?? (evt.type === 'portal_migration' ? 'migrated' : 'created'),
            mint: evt.mint ?? 'unknown',
            name: evt.name,
            symbol: evt.symbol,
            uri: evt.uri,
            pool: evt.pool,
            isMayhemMode: evt.isMayhemMode,
            txType: evt.txType,
            signature: evt.signature,
            marketCapSOL: evt.marketCapSOL,
            initialBuySOL: evt.initialBuySOL,
            dexId: evt.dexId,
            pairAddress: evt.pairAddress,
            priceUsd: evt.priceUsd,
            priceNative: evt.priceNative,
            marketCapUsd: evt.marketCapUsd,
            liquidityUsd: evt.liquidityUsd,
            fdv: evt.fdv,
            volume5mUsd: evt.volume5mUsd,
            volume1hUsd: evt.volume1hUsd,
            buys5m: evt.buys5m,
            sells5m: evt.sells5m,
            pairCreatedAt: evt.pairCreatedAt,
            imageUrl: evt.imageUrl,
            websiteUrl: evt.websiteUrl,
            socialHandle: evt.socialHandle,
            timestamp: evt.detectedAt,
            rawPayload: evt.rawPayload ?? JSON.stringify(evt, null, 2),
          });
          if (nextItem.stream === 'migrated') {
            setMigratedFeed((prev) => [nextItem, ...prev].slice(0, MAX_FEED_ITEMS));
          } else {
            setCreatedFeed((prev) => [nextItem, ...prev].slice(0, MAX_FEED_ITEMS));
          }
          return;
        }

        if (evt.type === 'portal_trade_metric' && evt.mint) {
          setFeed((prev) => prev.map((item) => item.mint === evt.mint ? {
            ...item,
            portalBuyVolumeSOL: evt.buyVolumeSOL,
            portalBuyCount: evt.buyCount,
          } : item));
        }
      },
      onError: setStatusMessage,
      onStatus: setSocketStatus,
    });

    return () => {
      ws?.close();
    };
  }, [session.initData]);

  useEffect(() => {
    if (!session.initData) {
      return;
    }

    let cancelled = false;
    const refreshWatchStats = async () => {
      try {
        const stats = await fetchPumpPortalWatchStats();
        if (!cancelled) {
          setPortalWatchStats(stats);
        }
      } catch {
        if (!cancelled) {
          setPortalWatchStats(null);
        }
      }
    };

    void refreshWatchStats();
    const intervalId = window.setInterval(() => {
      void refreshWatchStats();
    }, 15000);

    return () => {
      cancelled = true;
      window.clearInterval(intervalId);
    };
  }, [session.initData]);

  const currentPortalSourceFeed = activeStreamView === 'created' ? createdFeed : migratedFeed;
  const currentPortalFeed = useMemo(() => {
    if (portalVisibility === 'all') {
      return currentPortalSourceFeed;
    }
    return currentPortalSourceFeed.filter((item) => !item.isMayhemMode);
  }, [currentPortalSourceFeed, portalVisibility]);
  const hiddenMayhemCount = useMemo(() => currentPortalSourceFeed.reduce((count, item) => (
    item.isMayhemMode ? count + 1 : count
  ), 0), [currentPortalSourceFeed]);
  const visibleRowCount = activeStreamView === 'signals' ? feed.length : currentPortalFeed.length;

  useEffect(() => {
    if (visibleRowCount === 0) {
      setActiveRowIndex(0);
      return;
    }
    setActiveRowIndex((current) => Math.min(current, visibleRowCount - 1));
  }, [visibleRowCount]);

  useEffect(() => {
    let cancelled = false;

    async function loadSolUsdRate() {
      try {
        const response = await fetch('https://api.coinbase.com/v2/prices/SOL-USD/spot');
        if (!response.ok) {
          throw new Error(`SOL/USD spot request failed: ${response.status}`);
        }
        const payload = await response.json() as { data?: { amount?: string } };
        const nextRate = Number(payload.data?.amount ?? '');
        if (!cancelled && Number.isFinite(nextRate) && nextRate > 0) {
          setSolUsdRate(nextRate);
        }
      } catch {
        if (!cancelled) {
          setSolUsdRate(null);
        }
      }
    }

    void loadSolUsdRate();
    const refresh = window.setInterval(() => {
      void loadSolUsdRate();
    }, 5 * 60 * 1000);

    return () => {
      cancelled = true;
      window.clearInterval(refresh);
    };
  }, []);

  const stats = useMemo(() => {
    const liveCount = feed.filter((item) => item.source !== 'history').length;
    const personalCount = feed.filter((item) => item.source === 'personal').length;
    const highestRatio = feed.reduce((max, item) => Math.max(max, item.ratio ?? 0), 0);
    const watchedHits = feed.filter((item) => listeners.includes(item.mint)).length;
    return { liveCount, personalCount, highestRatio, watchedHits };
  }, [feed, listeners]);

  const signalTierABuys = useMemo(() => {
    const trackedRows = feed.filter((item) => typeof item.portalBuyVolumeSOL === 'number' && item.portalBuyVolumeSOL > 0);
    const totalPortalBuyVolume = trackedRows.reduce((sum, item) => sum + (item.portalBuyVolumeSOL ?? 0), 0);
    return {
      trackedCount: trackedRows.length,
      totalPortalBuyVolume,
    };
  }, [feed]);

  const portalStats = useMemo(() => {
    const current = currentPortalFeed;
    const namedCount = current.filter((item) => !!item.name || !!item.symbol).length;
    const withMarketCap = current.filter((item) => typeof item.marketCap === 'number' && item.marketCap > 0).length;
    const bestInitialBuy = current.reduce((max, item) => Math.max(max, item.initialBuySOL ?? 0), 0);
    return {
      count: current.length,
      namedCount,
      withMarketCap,
      bestInitialBuy,
    };
  }, [currentPortalFeed]);

  const leaderBoard = useMemo(() => {
    const totals = new Map<string, { mint: string; name?: string; symbol?: string; hits: number; maxRatio: number; wallets: number; marketCap?: number; age?: number }>();
    for (const item of feed) {
      const current = totals.get(item.mint) ?? { mint: item.mint, hits: 0, maxRatio: 0, wallets: 0 };
      current.name = item.name ?? current.name;
      current.symbol = item.symbol ?? current.symbol;
      current.hits += 1;
      current.maxRatio = Math.max(current.maxRatio, item.ratio ?? 0);
      current.wallets = Math.max(current.wallets, item.uniqueWallets ?? 0);
      current.marketCap = item.marketCap ?? current.marketCap;
      current.age = item.tokenAgeSeconds ?? current.age;
      totals.set(item.mint, current);
    }
    return Array.from(totals.values())
      .sort((a, b) => b.maxRatio - a.maxRatio)
      .slice(0, 5);
  }, [feed]);

  const portalLeaderBoard = useMemo(() => {
    const totals = new Map<string, { mint: string; name?: string; symbol?: string; hits: number; marketCap?: number; initialBuySOL: number; detectedAt?: string; txType?: string }>();
    for (const item of currentPortalFeed) {
      const current = totals.get(item.mint) ?? { mint: item.mint, hits: 0, initialBuySOL: 0 };
      current.name = item.name ?? current.name;
      current.symbol = item.symbol ?? current.symbol;
      current.hits += 1;
      current.marketCap = item.marketCap ?? current.marketCap;
      current.initialBuySOL = Math.max(current.initialBuySOL, item.initialBuySOL ?? 0);
      current.detectedAt = item.detectedAt ?? current.detectedAt;
      current.txType = item.txType ?? current.txType;
      totals.set(item.mint, current);
    }
    return Array.from(totals.values())
      .sort((a, b) => (b.marketCap ?? b.initialBuySOL) - (a.marketCap ?? a.initialBuySOL))
      .slice(0, 5);
  }, [currentPortalFeed]);

  const formatMarketCap = (marketCapSOL?: number) => {
    if (typeof marketCapSOL !== 'number' || Number.isNaN(marketCapSOL)) {
      return '--';
    }
    if (typeof solUsdRate === 'number' && Number.isFinite(solUsdRate) && solUsdRate > 0) {
      return formatCompactUSD(marketCapSOL * solUsdRate);
    }
    return `${formatNumber(marketCapSOL, 1)} SOL`;
  };

  const socketTone = socketStatus === 'connected' ? 'good' : socketStatus === 'error' || socketStatus === 'closed' ? 'bad' : 'warn';
  const fpsTone = fps >= 50 ? 'good' : fps >= 34 ? 'warn' : 'bad';
  const latencyTone = networkStrength;
  const socketText = socketStatus === 'connected'
    ? 'Streaming'
    : socketStatus === 'reconnecting'
      ? 'Reconnecting'
      : socketStatus === 'error'
        ? 'Socket error'
        : socketStatus === 'closed'
          ? 'Closed'
          : 'Connecting';
  const lastSeenLabel = activeStreamView === 'signals'
    ? (lastMessageAt ? formatTime(lastMessageAt) : 'No live flow yet')
    : (currentPortalFeed[0]?.detectedAt ? formatTime(currentPortalFeed[0].detectedAt) : 'No portal flow yet');
  const deskMood = activeStreamView === 'signals'
    ? (stats.personalCount > 0
      ? 'Personal watchlist is active and surfacing direct hits.'
      : stats.liveCount > 0
        ? 'The market is moving. Watch for strong ratio bursts and wallet depth.'
        : 'Quiet tape right now. The desk is waiting for live momentum.')
    : activeStreamView === 'created'
      ? 'PumpPortal is feeding fresh launches into the desk as soon as they appear.'
      : 'PumpPortal migration flow is live, so you can watch names as they cross into the next phase.';
  const topSignal = leaderBoard[0];
  const topPortalItem = portalLeaderBoard[0];
  const watchMode = listeners.length > 0 ? 'Focused watchlist' : 'Open market scan';

  const streamMetricCards = activeStreamView === 'signals'
    ? [
      { label: 'Live tape', value: String(stats.liveCount) },
      { label: 'Direct hits', value: String(stats.personalCount) },
      { label: 'Best burst', value: formatCompactRatio(stats.highestRatio) },
      { label: signalVolumeSource === 'portal' ? 'Tier A buys' : 'Watched flow', value: signalVolumeSource === 'portal' ? formatCompactSOL(signalTierABuys.totalPortalBuyVolume) : String(stats.watchedHits) },
    ]
    : [
      { label: activeStreamView === 'created' ? 'New arrivals' : 'New migrations', value: String(portalStats.count) },
      { label: 'Named tokens', value: String(portalStats.namedCount) },
      { label: 'Best opening buy', value: formatCompactSOL(portalStats.bestInitialBuy) },
      { label: 'With market cap', value: String(portalStats.withMarketCap) },
    ];

  const streamHeading = activeStreamView === 'signals'
    ? 'Signal Tape'
    : activeStreamView === 'created'
      ? 'Newly Created'
      : 'Newly Migrated';
  const streamIntro = activeStreamView === 'signals'
    ? 'Use this tape to separate genuine buying pressure from shallow reflex pops. Older tokens with real wallet spread usually hold attention better.'
    : activeStreamView === 'created'
      ? 'This PumpPortal lane shows new launches as they are created. Use it to scan naming quality, early buy size, and immediate metadata hygiene.'
      : 'This PumpPortal lane tracks fresh migrations. Use it to spot names that are graduating into the next liquidity stage.';
  const streamSummaryTitle = activeStreamView === 'signals'
    ? (topSignal ? tokenTitle(topSignal) : 'No leader yet')
    : (topPortalItem ? tokenTitle(topPortalItem) : 'No leader yet');
  const streamSummaryText = activeStreamView === 'signals'
    ? (topSignal ? `${formatCompactRatio(topSignal.maxRatio)} peak across ${topSignal.hits} hit${topSignal.hits === 1 ? '' : 's'}` : 'The desk needs more live flow before a leader stands out.')
    : activeStreamView === 'created'
      ? (topPortalItem ? `${formatMarketCap(topPortalItem.marketCap)} market cap with ${formatCompactSOL(topPortalItem.initialBuySOL)} opening buy.` : 'Waiting for fresh PumpPortal creation flow.')
      : (topPortalItem ? `${formatMarketCap(topPortalItem.marketCap)} market cap on the latest migration.` : 'Waiting for fresh PumpPortal migration flow.');
  const streamBias = activeStreamView === 'signals'
    ? (signalVolumeSource === 'portal' ? 'PumpPortal Tier A buys layered onto the live tape' : stats.personalCount > 0 ? 'Personal flow active' : 'Broad market scan')
    : activeStreamView === 'created'
      ? 'Creation feed on PumpPortal'
      : 'Migration feed on PumpPortal';
  const currentCopyMint = activeStreamView === 'signals'
    ? feed[activeRowIndex]?.mint
    : currentPortalFeed[activeRowIndex]?.mint;

  const leaderPanel = (
    <section className="panel leader-panel">
      <div className="panel-heading panel-heading-split">
        <div className="panel-heading-copy">
          <h2>{activeStreamView === 'signals' ? 'Names in Motion' : activeStreamView === 'created' ? 'Fresh Launches' : 'Migration Board'}</h2>
          <span>{activeStreamView === 'signals' ? 'ranked by burst' : 'ranked by recency and cap'}{leaderDock === 'float' ? ' · drag near a rail to dock' : ' · docked automatically'}</span>
        </div>
        {leaderDock !== 'float' ? (
          <button
            type="button"
            className="undock-button"
            onClick={() => setLeaderDock('float')}
            title="Return leaderboard to floating mode"
            aria-label="Return leaderboard to floating mode"
          >
            <span aria-hidden="true">⤢</span>
          </button>
        ) : null}
      </div>
      <div className="panel-intro compact">
        <span className="eyebrow">Leader board</span>
        <p>{activeStreamView === 'signals' ? 'These are the names currently earning attention on the desk, not just printing isolated spikes.' : 'This board follows the currently selected PumpPortal lane so you can keep one ranked view while switching between creation and migration flow.'}</p>
      </div>
      <div className="leader-list">
        {(activeStreamView === 'signals' ? leaderBoard.length : portalLeaderBoard.length) === 0 ? (
          <p className="empty-copy">No ranked signals yet.</p>
        ) : activeStreamView === 'signals' ? leaderBoard.map((item) => (
          <div className="leader-row" key={item.mint}>
            <div>
              <button className="leader-copy" onClick={() => handleCopyMint(item.mint)}>
                {tokenTitle(item)}
              </button>
              <span>{shortMint(item.mint)} · {formatAge(item.age)} · {formatMarketCap(item.marketCap)} mcap</span>
              <span>{item.hits} hit{item.hits === 1 ? '' : 's'} · {item.wallets} wallets</span>
            </div>
            <em title={typeof item.maxRatio === 'number' ? `${formatNumber(item.maxRatio)}x` : ''}>{formatCompactRatio(item.maxRatio)}</em>
          </div>
        )) : portalLeaderBoard.map((item) => (
          <div className="leader-row" key={item.mint}>
            <div>
              <button className="leader-copy" onClick={() => handleCopyMint(item.mint)}>
                {tokenTitle(item)}
              </button>
              <span>{shortMint(item.mint)} · {formatTime(item.detectedAt)} · {formatMarketCap(item.marketCap)} mcap</span>
              <span>{item.hits} sighting{item.hits === 1 ? '' : 's'} · {item.txType ?? 'portal event'}</span>
            </div>
            <em>{formatCompactSOL(item.initialBuySOL)}</em>
          </div>
        ))}
      </div>
    </section>
  );

  async function handleCopyMint(mint: string) {
    try {
      await navigator.clipboard.writeText(mint);
      setStatusMessage(`Copied ${shortMint(mint)}`);
    } catch {
      setStatusMessage('Clipboard permission denied.');
    }
  }

  async function handleAddListener() {
    const mint = mintInput.trim();
    if (!mint) return;
    try {
      await addListener({ mint });
      setListeners(await fetchMyListeners());
      setPortalWatchStats(await fetchPumpPortalWatchStats());
      setMintInput('');
      setStatusMessage(`Listening to ${shortMint(mint)}`);
    } catch (err) {
      setStatusMessage(err instanceof Error ? err.message : 'Failed to add listener.');
    }
  }

  async function handleRemoveListener(mint: string) {
    try {
      await removeListener(mint);
      setListeners(await fetchMyListeners());
      setPortalWatchStats(await fetchPumpPortalWatchStats());
      setStatusMessage(`Removed ${shortMint(mint)}`);
    } catch (err) {
      setStatusMessage(err instanceof Error ? err.message : 'Failed to remove listener.');
    }
  }

  function handleTapeKeyDown(event: React.KeyboardEvent<HTMLElement>) {
    if (visibleRowCount === 0) {
      return;
    }

    if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActiveRowIndex((current) => Math.min(current + 1, visibleRowCount - 1));
      return;
    }

    if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActiveRowIndex((current) => Math.max(current - 1, 0));
      return;
    }

    if (event.key === 'Home') {
      event.preventDefault();
      setActiveRowIndex(0);
      return;
    }

    if (event.key === 'End') {
      event.preventDefault();
      setActiveRowIndex(visibleRowCount - 1);
      return;
    }

    if (event.key === 'Enter') {
      event.preventDefault();
      void handleCopyMint(currentCopyMint ?? '');
    }
  }

  return (
    <div className="terminal-shell" data-theme={theme}>
      <header className="topbar">
        <div className="brand-block">
          <span className="mark">SW</span>
          <div>
            <h1>Sol Whisperer</h1>
            <p>Solana signal desk for fast-moving launches and watchlisted flow</p>
          </div>
        </div>

        <nav className="desk-tabs" aria-label="Dashboard views">
          {tabs.map((tab) => (
            <button
              key={tab}
              className={tab === activeTab ? 'active' : ''}
              onClick={() => setActiveTab(tab)}
            >
              {tab}
            </button>
          ))}
        </nav>

        <div className="status-cluster">
          <button
            type="button"
            className="theme-toggle"
            onClick={() => setTheme((current) => current === 'dark' ? 'light' : 'dark')}
            aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            aria-pressed={theme === 'dark'}
          >
            <span>{theme === 'dark' ? 'Dark' : 'Light'}</span>
          </button>
          <span className={`status-dot ${socketTone}`}>{socketText}</span>
          <span className={`status-dot ${isAuthed ? 'good' : 'warn'}`}>
            {isAuthed ? 'Telegram verified' : 'Public mode'}
          </span>
        </div>
      </header>

      <section className="desk-note" aria-label="Desk summary">
        <div>
          <span className="eyebrow">Desk note</span>
          <strong>{deskMood}</strong>
          <p>Read the tape for conviction, not noise. Priority, wallet breadth, and route quality matter more than raw volume alone.</p>
        </div>
        <div className="desk-note-meta">
          <span><strong>Last signal</strong>{lastSeenLabel}</span>
          <span><strong>Mode</strong>{isAuthed ? 'Personal desk' : 'Public read-only'}</span>
        </div>
      </section>

      {statusMessage && (
        <section className="notice" aria-live="polite">
          <span>{statusMessage}</span>
          <button onClick={() => setStatusMessage('')}>Dismiss</button>
        </section>
      )}

      {authWarning && (
        <section className="auth-strip">
          <div>
            <strong>Telegram session missing</strong>
            <span>{authWarning}</span>
          </div>
          <a href="https://t.me/BotFather" target="_blank" rel="noreferrer">Bot setup</a>
        </section>
      )}

      <main className="desk-grid">
        <aside className="left-rail">
          <section className="panel">
            <div className="panel-heading">
              <h2>Watchlist</h2>
              <span>{listeners.length} active names</span>
            </div>
            <div className="panel-intro compact rail-intro">
              <span className="eyebrow">Workflow</span>
              <p>Pin names you want routed fast. This rail is for deliberate focus, not passive browsing.</p>
            </div>
            <div className="listener-form">
              <input
                value={mintInput}
                onChange={(event) => setMintInput(event.target.value)}
                placeholder="Paste token mint"
                aria-label="Token mint address"
              />
              <button onClick={handleAddListener} disabled={!isAuthed || !mintInput.trim()}>
                Pin
              </button>
            </div>
            <div className="rail-strip">
              <span><strong>Mode</strong>{watchMode}</span>
              <span><strong>Tier bias</strong>{listeners.length > 0 ? 'Tier A on watched names' : 'Tier B until promoted'}</span>
            </div>
            <div className="watch-list">
              {listeners.length === 0 ? (
                <p className="empty-copy">No names pinned yet.</p>
              ) : listeners.map((mint) => (
                <div className="watch-row" key={mint}>
                  <button className="mint-button" onClick={() => handleCopyMint(mint)}>
                    {shortMint(mint)}
                  </button>
                  <button className="ghost-button" onClick={() => handleRemoveListener(mint)}>
                    Unpin
                  </button>
                </div>
              ))}
            </div>
          </section>

          <section className="panel">
            <div className="panel-heading">
              <h2>Desk Health</h2>
              <span>{lastMessageAt ? formatTime(lastMessageAt) : 'idle'}</span>
            </div>
            <div className="panel-intro compact rail-intro">
              <span className="eyebrow">Readiness</span>
              <p>Before trusting a burst, confirm the route is current and your personal path is actually armed.</p>
            </div>
            <div className="health-list">
              <div><span>Socket</span><strong>{socketText}</strong></div>
              <div><span>Identity</span><strong>{isAuthed ? 'Telegram' : 'Public'}</strong></div>
              <div><span>Alert route</span><strong>Default chat</strong></div>
              <div><span>Personal path</span><strong>{isAuthed ? 'Armed' : 'Locked'}</strong></div>
            </div>
          </section>

          <section className="panel compact-panel">
            <div className="panel-heading">
              <h2>Operator Notes</h2>
              <span>desk habits</span>
            </div>
            <div className="mini-list">
              <div><strong>1</strong><span>Wait for repeated wallet participation before trusting a burst.</span></div>
              <div><strong>2</strong><span>Watchlisted names deserve attention only if the route still feels current.</span></div>
              <div><strong>3</strong><span>Use the tape for triage first, then decide whether the move has real follow-through.</span></div>
            </div>
          </section>

          {leaderDock === 'left' && leaderPanel}
        </aside>

        <section className="center-stage">
          <div className="metric-strip">
            {streamMetricCards.map((card) => (
              <div key={card.label}>
                <span>{card.label}</span>
                <strong>{card.value}</strong>
              </div>
            ))}
          </div>

          {leaderDock === 'center' && leaderPanel}

          {activeTab === 'Signals' && (
            <section className="panel signal-panel">
              <div className="panel-heading panel-heading-split">
                <div className="panel-heading-copy">
                  <h2>{streamHeading}</h2>
                  <span>{visibleRowCount} entries</span>
                </div>
                <div className="stream-tabs" aria-label="Signal streams">
                  {streamTabs.map((tab) => (
                    <button
                      key={tab.id}
                      type="button"
                      className={activeStreamView === tab.id ? 'active' : ''}
                      onClick={() => setActiveStreamView(tab.id)}
                    >
                      {tab.label}
                    </button>
                  ))}
                </div>
              </div>
              {activeStreamView === 'signals' && (
                <div className="stream-control-row">
                  <span className="eyebrow">Volume source</span>
                  <div className="stream-tabs" aria-label="Signal volume source">
                    <button
                      type="button"
                      className={signalVolumeSource === 'desk' ? 'active' : ''}
                      onClick={() => setSignalVolumeSource('desk')}
                    >
                      Desk Volume
                    </button>
                    <button
                      type="button"
                      className={signalVolumeSource === 'portal' ? 'active' : ''}
                      onClick={() => setSignalVolumeSource('portal')}
                    >
                      PumpPortal Buys
                    </button>
                  </div>
                  <span className="control-copy">PumpPortal buy flow is treated as Tier A enrichment for current live names.</span>
                </div>
              )}
              {activeStreamView !== 'signals' && (
                <div className="stream-control-row">
                  <span className="eyebrow">Mayhem mode</span>
                  <div className="stream-tabs" aria-label="PumpPortal mayhem visibility">
                    <button
                      type="button"
                      className={portalVisibility === 'clean' ? 'active' : ''}
                      onClick={() => setPortalVisibility('clean')}
                    >
                      Hide mayhem
                    </button>
                    <button
                      type="button"
                      className={portalVisibility === 'all' ? 'active' : ''}
                      onClick={() => setPortalVisibility('all')}
                    >
                      Show all
                    </button>
                  </div>
                  <span className="control-copy">
                    {portalVisibility === 'clean'
                      ? `${hiddenMayhemCount} mayhem token${hiddenMayhemCount === 1 ? '' : 's'} filtered from this stream.`
                      : 'Mayhem names stay visible, but carry a dedicated state badge in the token line.'}
                  </span>
                </div>
              )}
              <div className="panel-intro">
                <span className="eyebrow">Live read</span>
                <p>{streamIntro}</p>
              </div>
              <div className="tape-summary">
                <div>
                  <span className="eyebrow">Top name right now</span>
                  <strong>{streamSummaryTitle}</strong>
                  <p>{streamSummaryText}</p>
                </div>
                <div className="tape-summary-grid">
                  <span><strong>Bias</strong>{streamBias}</span>
                  <span><strong>Read style</strong>{activeStreamView === 'signals' ? 'Favor wallet breadth over raw burst' : 'Scan metadata hygiene, opening size, and recency'}</span>
                </div>
              </div>
              {activeStreamView === 'signals' ? (
                <div
                  className="tape-table"
                  role="grid"
                  tabIndex={0}
                  aria-label="Signal tape"
                  onKeyDown={handleTapeKeyDown}
                >
                  <div className="tape-head">
                    <span>Priority</span>
                    <span>Token</span>
                    <span>Age</span>
                    <span>Ratio</span>
                    <span>Grade</span>
                    <span>Floor</span>
                    <span>MCap</span>
                    <span>Wallets</span>
                    <span>{signalVolumeSource === 'portal' ? 'Portal Buys' : 'Volume'}</span>
                    <span>Tier</span>
                    <span>Time</span>
                  </div>
                  {feed.length === 0 ? (
                    <div className="empty-state">Waiting for live PumpDev events or recent spike history.</div>
                  ) : feed.map((item, index) => (
                    <article
                      className={`tape-row ${item.priority.toLowerCase()} ${index === activeRowIndex ? 'active-row' : ''}`}
                      key={item.id}
                      role="row"
                      aria-selected={index === activeRowIndex}
                      tabIndex={-1}
                      onMouseEnter={() => setActiveRowIndex(index)}
                    >
                      <span className="priority-cell" title={priorityBrief(item.priority)}>{item.priority} <small>{priorityLabel(item.priority)}</small></span>
                      <button className="token-cell" onClick={() => handleCopyMint(item.mint)}>
                        <strong>{tokenTitle(item)}</strong>
                        <small>
                          {shortMint(item.mint)}{item.source === 'personal' ? ' · direct' : item.source === 'history' ? ' · archive' : ' · live'}
                          {typeof item.portalBuyVolumeSOL === 'number' && item.portalBuyVolumeSOL > 0 ? <span className="tier-a-badge">Tier A</span> : null}
                          {item.sourceLabel ? <span className="tier-a-badge watched-source">{item.sourceLabel}</span> : null}
                        </small>
                      </button>
                      <span className="numeric-cell">{formatAge(item.tokenAgeSeconds)}</span>
                      <strong className="signal-value" title={typeof item.ratio === 'number' ? `${formatNumber(item.ratio)}x` : ''}>{formatCompactRatio(item.ratio)}</strong>
                      <span className={`grade-badge ${(item.entryGrade ?? '').toLowerCase()}`}>{formatGrade(item.entryGrade)}</span>
                      <span className="numeric-cell metadata-cell" title={typeof item.floorConfidence === 'number' ? `${formatNumber(item.floorConfidence, 3)}` : ''}>{formatFloorConfidence(item.floorConfidence)}</span>
                      <span className="numeric-cell signal-column">{formatMarketCap(item.marketCap)}</span>
                      <span className="numeric-cell metadata-cell">{item.uniqueWallets ?? '--'}</span>
                      <span className="numeric-cell signal-column">{signalVolumeSource === 'portal' ? formatCompactSOL(item.portalBuyVolumeSOL) : `${formatNumber(item.windowVolume, 4)} SOL`}</span>
                      <span className="numeric-cell metadata-cell">{signalVolumeSource === 'portal' ? `${item.portalBuyCount ?? 0} buy${(item.portalBuyCount ?? 0) === 1 ? '' : 's'}` : item.tier ?? (listeners.includes(item.mint) ? 'A' : 'B')}</span>
                      <span className="numeric-cell metadata-cell">{formatTime(item.detectedAt)}</span>
                    </article>
                  ))}
                </div>
              ) : (
                <div
                  className="portal-table"
                  role="grid"
                  tabIndex={0}
                  aria-label={activeStreamView === 'created' ? 'PumpPortal newly created stream' : 'PumpPortal newly migrated stream'}
                  onKeyDown={handleTapeKeyDown}
                >
                  <div className={`portal-head ${activeStreamView === 'migrated' ? 'migrated-head' : ''}`}>
                    <span>Feed</span>
                    <span>Token</span>
                    <span>{activeStreamView === 'migrated' ? 'Liquidity' : 'Initial Buy'}</span>
                    <span>MCap</span>
                    <span>{activeStreamView === 'migrated' ? '5m Vol' : 'Tx'}</span>
                    <span>Seen</span>
                  </div>
                  {currentPortalFeed.length === 0 ? (
                    <div className="empty-state">Waiting for PumpPortal {activeStreamView === 'created' ? 'creation' : 'migration'} events.</div>
                  ) : currentPortalFeed.map((item, index) => (
                    <article
                      className={`portal-row ${activeStreamView === 'migrated' ? 'migrated-row' : ''} ${index === activeRowIndex ? 'active-row' : ''}`}
                      key={item.id}
                      role="row"
                      aria-selected={index === activeRowIndex}
                      tabIndex={-1}
                      onMouseEnter={() => setActiveRowIndex(index)}
                    >
                      <span className="priority-cell portal-badge">{item.stream === 'created' ? 'NEW' : 'MIG'} <small>{item.txType ?? 'portal'}</small></span>
                      <button className="token-cell" onClick={() => handleCopyMint(item.mint)}>
                        <strong>{tokenTitle(item)}</strong>
                        <small>
                          <span>{shortMint(item.mint)}</span>
                          {item.pool ? <span className="portal-inline-pill">{item.pool}</span> : null}
                          {item.dexId ? <span className="portal-inline-pill">{item.dexId}</span> : null}
                          {activeStreamView === 'migrated' && item.pairCreatedAt ? <span className="portal-inline-pill">pair {formatPairAge(item.pairCreatedAt)}</span> : null}
                          {item.isMayhemMode ? <span className="portal-inline-pill mayhem">Mayhem</span> : null}
                          {!item.isMayhemMode && item.uri ? <span className="portal-inline-pill">Metadata</span> : null}
                          {!item.isMayhemMode && !item.uri ? <span className="portal-inline-pill muted">No URI</span> : null}
                          {activeStreamView === 'migrated' && item.websiteUrl ? <span className="portal-inline-pill">site</span> : null}
                          {activeStreamView === 'migrated' && item.socialHandle ? <span className="portal-inline-pill">social</span> : null}
                        </small>
                      </button>
                      <span className="numeric-cell signal-column">{activeStreamView === 'migrated' ? formatCompactUSD(item.liquidityUsd) : formatCompactSOL(item.initialBuySOL)}</span>
                      <span className="numeric-cell signal-column">{activeStreamView === 'migrated' ? formatCompactUSD(item.marketCapUsd) : formatMarketCap(item.marketCap)}</span>
                      <span className="numeric-cell metadata-cell">{activeStreamView === 'migrated' ? formatCompactUSD(item.volume5mUsd) : item.txType ?? '--'}</span>
                      <span className="numeric-cell metadata-cell">{formatTime(item.detectedAt)}</span>
                    </article>
                  ))}
                </div>
              )}
            </section>
          )}

          {activeTab === 'Listeners' && (
            <section className="panel detail-panel">
              <div className="panel-heading">
                <h2>Watchlist Routing</h2>
                <span>Tier A for watched names</span>
              </div>
              <p className="panel-copy">
                Anything you watch gets faster routing and personal fanout, so the desk feels tighter when a name you care about starts to wake up.
              </p>
              <div className="listener-grid">
                {listeners.map((mint) => (
                  <article key={mint}>
                    <span>{shortMint(mint)}</span>
                    <strong>Tier A</strong>
                    <button onClick={() => handleCopyMint(mint)}>Copy mint</button>
                  </article>
                ))}
              </div>
            </section>
          )}

          {activeTab === 'Risk' && (
            <section className="panel detail-panel">
              <div className="panel-heading">
                <h2>Execution Discipline</h2>
                <span>Dry-run before size</span>
              </div>
              <div className="risk-grid">
                <div><strong>Trade from a separate wallet</strong><span>Keep monitoring, funding, and execution isolated so one mistake does not contaminate the whole desk.</span></div>
                <div><strong>Respect route latency</strong><span>Before enabling execution, watch reconnect behavior and make sure the tape still feels current under pressure.</span></div>
                <div><strong>Be explicit about Jito mode</strong><span>No-auth mode is fine for rehearsal. Real execution deserves a deliberate, authenticated route.</span></div>
              </div>
            </section>
          )}

          {activeTab === 'Settings' && (
            <section className="panel detail-panel">
              <div className="panel-heading">
                <h2>Desk Session</h2>
                <span>{session.source}</span>
              </div>
              <div className="health-list wide">
                <div><span>Telegram auth</span><strong>{isAuthed ? 'Verified initData' : 'Missing'}</strong></div>
                <div><span>Websocket path</span><strong>{isAuthed ? '/ws/stream' : '/ws/public'}</strong></div>
                <div><span>Alert delivery</span><strong>Bot default chat</strong></div>
              </div>
              <div className="panel-heading subheading">
                <h2>PumpPortal Watch Stats</h2>
                <span>{portalWatchStats?.activeMints ?? 0} active mints</span>
              </div>
              <div className="listener-grid watch-stats-grid">
                {portalWatchStats && Object.keys(portalWatchStats.watcherCounts).length > 0 ? Object.entries(portalWatchStats.watcherCounts)
                  .sort((a, b) => b[1] - a[1])
                  .map(([mint, count]) => (
                    <article key={mint}>
                      <span>{shortMint(mint)}</span>
                      <strong>{count} watcher{count === 1 ? '' : 's'}</strong>
                      <button onClick={() => handleCopyMint(mint)}>Copy mint</button>
                    </article>
                  )) : (
                  <article>
                    <span>No shared PumpPortal watches</span>
                    <strong>0 watcher groups</strong>
                    <button disabled>Waiting for watched mints</button>
                  </article>
                )}
              </div>
            </section>
          )}
        </section>

        <aside className="right-rail">
          {leaderDock === 'right' && leaderPanel}
          <section className="panel">
            <div className="panel-heading">
              <h2>How a Signal Travels</h2>
              <span>from ingest to alert</span>
            </div>
            <div className="panel-intro compact">
              <span className="eyebrow">Pipeline</span>
              <p>The goal is simple: reduce delay, preserve confidence, and get watched flow in front of you before the crowd catches up.</p>
            </div>
            <div className="alert-path">
              <div><span>1</span><p>Raw market flow is normalized into the processor.</p></div>
              <div><span>2</span><p>The tracker compares it against recent baseline behavior.</p></div>
              <div><span>3</span><p>Watched names are promoted into personal P1 fanout.</p></div>
              <div><span>4</span><p>The bot mirrors the same signal into Telegram delivery.</p></div>
            </div>
          </section>
        </aside>

        {leaderDock === 'float' && (
          <aside
            ref={floatingLeaderRef}
            className={`floating-leader ${isDragging ? 'dragging' : ''}`}
            style={{
              transform: `translate(${dockPos.x}px, ${dockPos.y}px)`,
              transition: isDragging ? 'none' : 'transform 0.2s ease-out',
            }}
            onMouseDown={handleDockMouseDown}
          >
            {leaderPanel}
          </aside>
        )}

        <div
          className={`latency-indicator ${latencyCorner} ${isLatencyDragging ? 'dragging' : ''}`}
          onMouseDown={handleLatencyMouseDown}
          role="status"
          aria-live="polite"
          aria-label={`Interface framerate ${fps} frames per second and network round trip ${networkRttMs ?? 'unknown'} milliseconds`}
        >
          <div className="latency-pill-stack">
            <span className={`status-dot ${fpsTone}`}>{fps > 0 ? `${fps} FPS` : 'FPS --'}</span>
            <span className={`status-dot ${latencyTone}`}>{networkRttMs !== null ? `${networkRttMs} ms` : 'RTT --'}</span>
            <span className={`status-dot ${latencyTone}`}>{networkLabel}</span>
          </div>
        </div>
      </main>
    </div>
  );
}

function mapPortalEvent(item: PumpPortalEvent): PortalFeedItem {
  return {
    id: crypto.randomUUID(),
    stream: item.stream,
    mint: item.mint,
    name: item.name,
    symbol: item.symbol,
    uri: item.uri,
    pool: item.pool,
    isMayhemMode: item.isMayhemMode,
    txType: item.txType,
    signature: item.signature,
    marketCap: item.marketCapSOL,
    initialBuySOL: item.initialBuySOL,
    dexId: item.dexId,
    pairAddress: item.pairAddress,
    priceUsd: item.priceUsd,
    priceNative: item.priceNative,
    marketCapUsd: item.marketCapUsd,
    liquidityUsd: item.liquidityUsd,
    fdv: item.fdv,
    volume5mUsd: item.volume5mUsd,
    volume1hUsd: item.volume1hUsd,
    buys5m: item.buys5m,
    sells5m: item.sells5m,
    pairCreatedAt: item.pairCreatedAt,
    imageUrl: item.imageUrl,
    websiteUrl: item.websiteUrl,
    socialHandle: item.socialHandle,
    detectedAt: item.timestamp,
    rawPayload: item.rawPayload ?? JSON.stringify(item, null, 2),
  };
}
