import { useEffect, useMemo, useState, useRef } from 'react';
import { addListener, fetchMyListeners, fetchRecentSpikes, removeListener } from '../lib/api';
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
  marketCap?: number;
  tokenCreatedAt?: string;
  tokenAgeSeconds?: number;
  floorConfidence?: number;
  entryGrade?: string;
  detectedAt?: string;
  tier?: string;
  rpcEndpoint?: string;
  rawPayload: string;
};

const MAX_FEED_ITEMS = 32;
const tabs = ['Signals', 'Listeners', 'Risk', 'Settings'] as const;

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

function tokenTitle(item: Pick<FeedItem, 'name' | 'symbol' | 'mint'>): string {
  if (item.name && item.symbol) return `${item.name} / ${item.symbol}`;
  if (item.name) return item.name;
  if (item.symbol) return item.symbol;
  return shortMint(item.mint);
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

export function App() {
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>('Signals');
  const [feed, setFeed] = useState<FeedItem[]>([]);
  const [listeners, setListeners] = useState<string[]>([]);
  const [mintInput, setMintInput] = useState('');
  const [authWarning, setAuthWarning] = useState('');
  const [statusMessage, setStatusMessage] = useState('');
  const [socketStatus, setSocketStatus] = useState<SocketStatus>('connecting');
  const [lastMessageAt, setLastMessageAt] = useState<string>('');
  const [solUsdRate, setSolUsdRate] = useState<number | null>(null);
  const [dockPos, setDockPos] = useState<{ x: number; y: number }>({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const dragRef = useRef<{ startX: number; startY: number; offsetX: number; offsetY: number }>({ startX: 0, startY: 0, offsetX: 0, offsetY: 0 });

  const session = useMemo(() => getTelegramSession(), []);
  const isAuthed = !!session.initData;

  // Load dock position from localStorage on mount
  useEffect(() => {
    const stored = localStorage.getItem('signalLeadersDockPos');
    if (stored) {
      try {
        setDockPos(JSON.parse(stored));
      } catch {
        // ignore parse errors
      }
    }
  }, []);

  // Handle dock drag
  const handleDockMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('button, input, a')) return; // Don't drag when clicking buttons
    setIsDragging(true);
    dragRef.current = {
      startX: e.clientX,
      startY: e.clientY,
      offsetX: dockPos.x,
      offsetY: dockPos.y,
    };
  };

  useEffect(() => {
    if (!isDragging) return;

    const handleMouseMove = (e: MouseEvent) => {
      const deltaX = e.clientX - dragRef.current.startX;
      const deltaY = e.clientY - dragRef.current.startY;
      const newX = dragRef.current.offsetX + deltaX;
      const newY = dragRef.current.offsetY + deltaY;
      setDockPos({ x: newX, y: newY });
    };

    const handleMouseUp = () => {
      setIsDragging(false);
      // Save position to localStorage
      localStorage.setItem('signalLeadersDockPos', JSON.stringify(dockPos));
    };

    document.addEventListener('mousemove', handleMouseMove);
    document.addEventListener('mouseup', handleMouseUp);
    return () => {
      document.removeEventListener('mousemove', handleMouseMove);
      document.removeEventListener('mouseup', handleMouseUp);
    };
  }, [isDragging, dockPos]);

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

    const ws = openLiveSocket({
      onEvent: (evt) => {
        if (evt.type !== 'volume_spike' && evt.type !== 'personal_listener_spike') return;
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
          marketCap: evt.marketCapSOL,
          tokenCreatedAt: evt.tokenCreatedAt,
          tokenAgeSeconds: evt.tokenAgeSeconds,
          floorConfidence: evt.floorConfidence,
          entryGrade: evt.entryGrade,
          detectedAt: evt.detectedAt,
          tier: evt.tier,
          rpcEndpoint: evt.rpcEndpoint,
          rawPayload: JSON.stringify(evt, null, 2),
        }, ...prev].slice(0, MAX_FEED_ITEMS));
      },
      onError: setStatusMessage,
      onStatus: setSocketStatus,
    });

    return () => {
      ws?.close();
    };
  }, [session.initData]);

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
  const socketText = socketStatus === 'connected'
    ? 'Streaming'
    : socketStatus === 'reconnecting'
      ? 'Reconnecting'
      : socketStatus === 'error'
        ? 'Socket error'
        : socketStatus === 'closed'
          ? 'Closed'
          : 'Connecting';

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
      setStatusMessage(`Removed ${shortMint(mint)}`);
    } catch (err) {
      setStatusMessage(err instanceof Error ? err.message : 'Failed to remove listener.');
    }
  }

  return (
    <div className="terminal-shell">
      <header className="topbar">
        <div className="brand-block">
          <span className="mark">SW</span>
          <div>
            <h1>Sol Whisperer</h1>
            <p>Live Solana meme-token spike desk</p>
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
          <span className={`status-dot ${socketTone}`}>{socketText}</span>
          <span className={`status-dot ${isAuthed ? 'good' : 'warn'}`}>
            {isAuthed ? 'Telegram verified' : 'Public mode'}
          </span>
        </div>
      </header>

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
              <h2>Watch Controls</h2>
              <span>{listeners.length} active</span>
            </div>
            <div className="listener-form">
              <input
                value={mintInput}
                onChange={(event) => setMintInput(event.target.value)}
                placeholder="Token mint address"
                aria-label="Token mint address"
              />
              <button onClick={handleAddListener} disabled={!isAuthed || !mintInput.trim()}>
                Add
              </button>
            </div>
            <div className="watch-list">
              {listeners.length === 0 ? (
                <p className="empty-copy">No personal mints yet.</p>
              ) : listeners.map((mint) => (
                <div className="watch-row" key={mint}>
                  <button className="mint-button" onClick={() => handleCopyMint(mint)}>
                    {shortMint(mint)}
                  </button>
                  <button className="ghost-button" onClick={() => handleRemoveListener(mint)}>
                    Remove
                  </button>
                </div>
              ))}
            </div>
          </section>

          <section className="panel">
            <div className="panel-heading">
              <h2>Route Health</h2>
              <span>{lastMessageAt ? formatTime(lastMessageAt) : 'waiting'}</span>
            </div>
            <div className="health-list">
              <div><span>Socket</span><strong>{socketText}</strong></div>
              <div><span>Identity</span><strong>{isAuthed ? 'Telegram' : 'Public'}</strong></div>
              <div><span>Alerts</span><strong>Default chat</strong></div>
              <div><span>Personal fanout</span><strong>{isAuthed ? 'Enabled' : 'Locked'}</strong></div>
            </div>
          </section>
        </aside>

        <section className="center-stage">
          <div className="metric-strip">
            <div>
              <span>Live hits</span>
              <strong>{stats.liveCount}</strong>
            </div>
            <div>
              <span>Personal P1</span>
              <strong>{stats.personalCount}</strong>
            </div>
            <div>
              <span>Peak ratio</span>
              <strong>{formatCompactRatio(stats.highestRatio)}</strong>
            </div>
            <div>
              <span>Watched hits</span>
              <strong>{stats.watchedHits}</strong>
            </div>
          </div>

          {activeTab === 'Signals' && (
            <section className="panel signal-panel">
              <div className="panel-heading">
                <h2>Spike Tape</h2>
                <span>{feed.length} records</span>
              </div>
              <div className="tape-table">
                <div className="tape-head">
                  <span>Priority</span>
                  <span>Token</span>
                  <span>Age</span>
                  <span>Ratio</span>
                  <span>Grade</span>
                  <span>Floor</span>
                  <span>MCap</span>
                  <span>Wallets</span>
                  <span>Volume</span>
                  <span>Tier</span>
                  <span>Time</span>
                </div>
                {feed.length === 0 ? (
                  <div className="empty-state">Waiting for live PumpDev events or recent spike history.</div>
                ) : feed.map((item) => (
                  <article className={`tape-row ${item.priority.toLowerCase()}`} key={item.id}>
                    <span className="priority-cell">{item.priority} <small>{priorityLabel(item.priority)}</small></span>
                    <button className="token-cell" onClick={() => handleCopyMint(item.mint)}>
                      <strong>{tokenTitle(item)}</strong>
                      <small>{shortMint(item.mint)}</small>
                    </button>
                    <span>{formatAge(item.tokenAgeSeconds)}</span>
                    <strong title={typeof item.ratio === 'number' ? `${formatNumber(item.ratio)}x` : ''}>{formatCompactRatio(item.ratio)}</strong>
                    <span className={`grade-badge ${(item.entryGrade ?? '').toLowerCase()}`}>{formatGrade(item.entryGrade)}</span>
                    <span title={typeof item.floorConfidence === 'number' ? `${formatNumber(item.floorConfidence, 3)}` : ''}>{formatFloorConfidence(item.floorConfidence)}</span>
                    <span>{formatMarketCap(item.marketCap)}</span>
                    <span>{item.uniqueWallets ?? '--'}</span>
                    <span>{formatNumber(item.windowVolume, 4)} SOL</span>
                    <span>{item.tier ?? (listeners.includes(item.mint) ? 'A' : 'B')}</span>
                    <span>{formatTime(item.detectedAt)}</span>
                  </article>
                ))}
              </div>
            </section>
          )}

          {activeTab === 'Listeners' && (
            <section className="panel detail-panel">
              <div className="panel-heading">
                <h2>Listener Routing</h2>
                <span>Tier A on watched mints</span>
              </div>
              <p className="panel-copy">
                Watched mints receive personal websocket fanout at P1. General market spikes stay on the public tape.
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
                <h2>Execution Risk</h2>
                <span>Dry-run first</span>
              </div>
              <div className="risk-grid">
                <div><strong>Use a separate wallet</strong><span>Keep operational funds isolated from the listener wallet.</span></div>
                <div><strong>Validate route latency</strong><span>Watch websocket delay and PumpDev reconnect behavior before enabling execution.</span></div>
                <div><strong>Confirm Jito mode</strong><span>No-auth mode is for testing. Use explicit auth for production execution.</span></div>
              </div>
            </section>
          )}

          {activeTab === 'Settings' && (
            <section className="panel detail-panel">
              <div className="panel-heading">
                <h2>Session</h2>
                <span>{session.source}</span>
              </div>
              <div className="health-list wide">
                <div><span>Telegram auth</span><strong>{isAuthed ? 'Verified initData' : 'Missing'}</strong></div>
                <div><span>Websocket path</span><strong>{isAuthed ? '/ws/stream' : '/ws/public'}</strong></div>
                <div><span>Alert delivery</span><strong>Bot default chat</strong></div>
              </div>
            </section>
          )}
        </section>

        <aside
          className={`right-rail ${isDragging ? 'dragging' : ''}`}
          style={{
            transform: `translate(${dockPos.x}px, ${dockPos.y}px)`,
            transition: isDragging ? 'none' : 'transform 0.2s ease-out',
          }}
          onMouseDown={handleDockMouseDown}
        >
          <section className="panel">
            <div className="panel-heading">
              <h2>Signal Leaders</h2>
              <span>max ratio</span>
            </div>
            <div className="leader-list">
              {leaderBoard.length === 0 ? (
                <p className="empty-copy">No ranked signals yet.</p>
              ) : leaderBoard.map((item) => (
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
              ))}
            </div>
          </section>

          <section className="panel">
            <div className="panel-heading">
              <h2>Alert Path</h2>
              <span>current build</span>
            </div>
            <div className="alert-path">
              <div><span>1</span><p>PumpDev event parsed into volume processor.</p></div>
              <div><span>2</span><p>Spike is broadcast to websocket clients.</p></div>
              <div><span>3</span><p>Watched mints fan out as personal P1 events.</p></div>
              <div><span>4</span><p>Telegram bot sends spike text to default chat.</p></div>
            </div>
          </section>
        </aside>
      </main>
    </div>
  );
}
