import { useEffect, useMemo, useState } from 'react';
import { addListener, fetchMyListeners, fetchRecentSpikes, removeListener } from '../lib/api';
import { ensureTelegramReady, getTelegramSession } from '../lib/telegram';
import { openLiveSocket, type SocketStatus } from '../lib/ws';

type Priority = 'P1' | 'P2' | 'P3' | 'P4';

type FeedItem = {
  id: string;
  priority: Priority;
  title: string;
  detail: string;
};

const MAX_FEED_ITEMS = 24;

const tabs = ['Live Feed', 'My Listeners', 'Sniping History', 'Settings'] as const;

export function App() {
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>('Live Feed');
  const [darkMode, setDarkMode] = useState(true);
  const [feed, setFeed] = useState<FeedItem[]>([]);
  const [listeners, setListeners] = useState<string[]>([]);
  const [mintInput, setMintInput] = useState('');
  const [authWarning, setAuthWarning] = useState('');
  const [statusMessage, setStatusMessage] = useState('');
  const [socketStatus, setSocketStatus] = useState<SocketStatus>('connecting');

  const rootClass = useMemo(() => (darkMode ? 'theme-dark' : 'theme-light'), [darkMode]);

  useEffect(() => {
    ensureTelegramReady();
    const tg = getTelegramSession();
    if (!tg.initData) {
      setAuthWarning('Telegram session is missing. Open inside Telegram WebApp or set VITE_TELEGRAM_INIT_DATA for secure local testing.');
    }

    fetchRecentSpikes()
      .then((items) => {
        const mapped = items.map((item) => ({
          id: item.id,
          priority: 'P3' as Priority,
          title: `Spike ${item.mint.slice(0, 8)}...`,
          detail: `ratio=${item.ratio} uniqueWallets=${item.uniqueWallets}`
        }));
        setFeed(mapped.slice(0, MAX_FEED_ITEMS));
      })
      .catch((err: Error) => {
        setStatusMessage(`Spike bootstrap failed: ${err.message}`);
      });

    fetchMyListeners()
      .then(setListeners)
      .catch((err: Error) => {
        setStatusMessage(`Listener load failed: ${err.message}`);
      });

    const ws = openLiveSocket({
      onEvent: (evt) => {
        const priority = (evt.priority ?? 'P3') as Priority;
        const mint = evt.mint ?? 'unknown';
        const title = evt.type === 'personal_listener_spike' ? 'Personal Listener Spike' : 'Live Spike';
        const detail = evt.ratio ? `${mint} ratio=${evt.ratio}` : `${mint}`;

        setFeed((prev) => [{ id: crypto.randomUUID(), priority, title, detail }, ...prev].slice(0, MAX_FEED_ITEMS));
      },
      onError: (errMsg) => setStatusMessage(errMsg),
      onStatus: setSocketStatus
    });

    return () => {
      ws?.close();
    };
  }, []);

  const socketStatusLabel = useMemo(() => {
    switch (socketStatus) {
      case 'connected':
        return 'Live Connected';
      case 'reconnecting':
        return 'Reconnecting';
      case 'error':
        return 'Socket Error';
      case 'closed':
        return 'Socket Closed';
      default:
        return 'Connecting';
    }
  }, [socketStatus]);

  async function handleAddListener() {
    const mint = mintInput.trim();
    if (!mint) {
      return;
    }
    try {
      await addListener({ mint });
      const next = await fetchMyListeners();
      setListeners(next);
      setMintInput('');
      setStatusMessage('Listener added.');
    } catch (err) {
      setStatusMessage(`Failed to add listener: ${(err as Error).message}`);
    }
  }

  async function handleRemoveListener(mint: string) {
    try {
      await removeListener(mint);
      const next = await fetchMyListeners();
      setListeners(next);
      setStatusMessage('Listener removed.');
    } catch (err) {
      setStatusMessage(`Failed to remove listener: ${(err as Error).message}`);
    }
  }

  return (
    <div className={`app-shell ${rootClass}`}>
      <header className="hero">
        <div>
          <p className="badge">Solana Meme Radar</p>
          <h1>Sol Whisperer Dashboard</h1>
          <p className="subtitle">Low-latency spike intelligence with optional MEV-protected auto-sniping.</p>
          <p className={`socket-pill ${socketStatus}`}>{socketStatusLabel}</p>
        </div>
        <button className="mode-toggle" onClick={() => setDarkMode((v) => !v)}>
          {darkMode ? 'Light Mode' : 'Dark Mode'}
        </button>
      </header>

      <nav className="tabs">
        {tabs.map((tab) => (
          <button
            key={tab}
            className={activeTab === tab ? 'tab active' : 'tab'}
            onClick={() => setActiveTab(tab)}
          >
            {tab}
          </button>
        ))}
      </nav>

      <main className="content">
        {authWarning ? (
          <section className="risk-card">
            <h2>Telegram Auth Required</h2>
            <p>{authWarning}</p>
          </section>
        ) : null}

        {statusMessage ? (
          <section className="placeholder">
            <h3>Status</h3>
            <p>{statusMessage}</p>
          </section>
        ) : null}

        <section className="risk-card">
          <h2>Risk Warning</h2>
          <p>
            Auto-sniping and Jito tips involve real financial risk. Use a dedicated low-balance wallet and keep
            dry-run enabled until you validate every route and parameter.
          </p>
        </section>

        {activeTab === 'Live Feed' && (
          <section className="feed-grid">
            {feed.map((item) => (
              <article key={item.id} className={`feed-card ${item.priority.toLowerCase()}`}>
                <span className="priority-pill">{item.priority}</span>
                <h3>{item.title}</h3>
                <p>{item.detail}</p>
              </article>
            ))}
          </section>
        )}

        {activeTab === 'My Listeners' && (
          <section className="placeholder">
            <h3>My Listeners</h3>
            <div className="listener-controls">
              <input
                value={mintInput}
                onChange={(e) => setMintInput(e.target.value)}
                placeholder="Paste token mint"
              />
              <button onClick={handleAddListener}>Add Listener</button>
            </div>
            <div className="listener-list">
              {listeners.map((mint) => (
                <div className="listener-row" key={mint}>
                  <span>{mint}</span>
                  <button onClick={() => handleRemoveListener(mint)}>Remove</button>
                </div>
              ))}
            </div>
          </section>
        )}

        {activeTab !== 'Live Feed' && activeTab !== 'My Listeners' && (
          <section className="placeholder">
            <h3>{activeTab}</h3>
            <p>Scaffold ready. Real-time data binding is implemented in subsequent phases.</p>
          </section>
        )}
      </main>
    </div>
  );
}
