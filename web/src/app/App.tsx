import { useMemo, useState } from 'react';

type Priority = 'P1' | 'P2' | 'P3' | 'P4';

type FeedItem = {
  id: string;
  priority: Priority;
  title: string;
  detail: string;
};

const seedFeed: FeedItem[] = [
  { id: '1', priority: 'P1', title: 'Snipe Execution', detail: 'Dry-run bundle simulated for BONK2' },
  { id: '2', priority: 'P2', title: 'Migration Alert', detail: 'Token X moved from pump curve to LP' },
  { id: '3', priority: 'P3', title: 'General Spike', detail: 'Volume spike detected in 5m window' },
  { id: '4', priority: 'P4', title: 'Heartbeat', detail: 'All providers healthy' }
];

const tabs = ['Live Feed', 'My Listeners', 'Sniping History', 'Settings'] as const;

export function App() {
  const [activeTab, setActiveTab] = useState<(typeof tabs)[number]>('Live Feed');
  const [darkMode, setDarkMode] = useState(true);

  const rootClass = useMemo(() => (darkMode ? 'theme-dark' : 'theme-light'), [darkMode]);

  return (
    <div className={`app-shell ${rootClass}`}>
      <header className="hero">
        <div>
          <p className="badge">Solana Meme Radar</p>
          <h1>Sol Whisperer Dashboard</h1>
          <p className="subtitle">Low-latency spike intelligence with optional MEV-protected auto-sniping.</p>
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
        <section className="risk-card">
          <h2>Risk Warning</h2>
          <p>
            Auto-sniping and Jito tips involve real financial risk. Use a dedicated low-balance wallet and keep
            dry-run enabled until you validate every route and parameter.
          </p>
        </section>

        {activeTab === 'Live Feed' && (
          <section className="feed-grid">
            {seedFeed.map((item) => (
              <article key={item.id} className={`feed-card ${item.priority.toLowerCase()}`}>
                <span className="priority-pill">{item.priority}</span>
                <h3>{item.title}</h3>
                <p>{item.detail}</p>
              </article>
            ))}
          </section>
        )}

        {activeTab !== 'Live Feed' && (
          <section className="placeholder">
            <h3>{activeTab}</h3>
            <p>Scaffold ready. Real-time data binding is implemented in subsequent phases.</p>
          </section>
        )}
      </main>
    </div>
  );
}
