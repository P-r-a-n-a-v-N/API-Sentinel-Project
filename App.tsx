import { Shield, Activity, AlertTriangle, Clock, Zap, Ban, TrendingUp } from 'lucide-react';
import { useSummary, useTimeSeries, useEvents } from './hooks/useGatewayData';
import { StatCard } from './components/StatCard';
import { TrafficChart } from './components/TrafficChart';
import { EventsTable } from './components/EventsTable';
import { TopPaths } from './components/TopPaths';
import { StatusDistribution } from './components/StatusDistribution';
import { ConnectionBadge } from './components/ConnectionBadge';
import { format } from './utils/format';

export default function App() {
  const { data: summary, loading: summaryLoading, error: summaryError } = useSummary();
  const { data: series,  loading: seriesLoading  } = useTimeSeries(60);
  const { data: events,  loading: eventsLoading  } = useEvents(100);

  const blockRate = summary && summary.total_requests > 0
    ? ((summary.blocked_requests / summary.total_requests) * 100).toFixed(1)
    : '0.0';

  return (
    <div className="min-h-screen bg-[#0a0e1a] text-gray-100">
      {/* Header */}
      <header className="sticky top-0 z-50 border-b border-gray-800/60 bg-[#0a0e1a]/90 backdrop-blur-md">
        <div className="max-w-screen-2xl mx-auto px-6 py-3 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="relative">
              <Shield size={22} className="text-emerald-400" strokeWidth={1.5} />
              <span className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
            </div>
            <div>
              <h1 className="text-sm font-bold font-mono tracking-tight text-white leading-none">
                API <span className="text-emerald-400">SENTINEL</span>
              </h1>
              <p className="text-[10px] text-gray-600 font-mono leading-none mt-0.5">
                gateway observability dashboard
              </p>
            </div>
          </div>
          <div className="flex items-center gap-6">
            <div className="hidden md:flex items-center gap-4 text-[10px] font-mono text-gray-600">
              <span>UPSTREAM: <span className="text-gray-400">localhost:9000</span></span>
              <span>REFRESH: <span className="text-gray-400">2s</span></span>
            </div>
            <ConnectionBadge error={summaryError} loading={summaryLoading && !summary} />
          </div>
        </div>
      </header>

      <main className="max-w-screen-2xl mx-auto px-6 py-6 space-y-6">
        {/* KPI Row */}
        <section className="grid grid-cols-2 md:grid-cols-3 xl:grid-cols-6 gap-3">
          <StatCard label="Total Requests" value={summary?.total_requests ?? 0}
            accent="green" loading={summaryLoading && !summary} icon={<Activity size={14} />} />
          <StatCard label="Blocked" value={summary?.blocked_requests ?? 0}
            sub={`${blockRate}% of traffic`} accent="red" loading={summaryLoading && !summary}
            icon={<Ban size={14} />} />
          <StatCard label="Anomalies" value={summary?.anomaly_count ?? 0}
            accent="amber" loading={summaryLoading && !summary} icon={<AlertTriangle size={14} />} />
          <StatCard label="Avg Latency"
            value={format.latency(Math.round(summary?.avg_latency_ms ?? 0))}
            accent="blue" loading={summaryLoading && !summary} icon={<Clock size={14} />} />
          <StatCard label="Pass Rate"
            value={summary && summary.total_requests > 0
              ? `${(((summary.total_requests - summary.blocked_requests) / summary.total_requests) * 100).toFixed(1)}%`
              : '—'}
            accent="green" loading={summaryLoading && !summary} icon={<TrendingUp size={14} />} />
          <StatCard label="Top Path Hits" value={summary?.top_paths?.[0]?.count ?? 0}
            sub={summary?.top_paths?.[0]?.path ?? '—'}
            accent="blue" loading={summaryLoading && !summary} icon={<Zap size={14} />} />
        </section>

        {/* Traffic Timeline */}
        <section className="rounded-xl border border-gray-800 bg-gray-900/50 p-5">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xs font-mono uppercase tracking-widest text-gray-500">
              Traffic Timeline <span className="ml-2 text-gray-700">/ last 60s</span>
            </h2>
            <div className="flex items-center gap-3 text-[10px] font-mono text-gray-600">
              <span className="flex items-center gap-1"><span className="w-2 h-0.5 bg-emerald-500 rounded" /> requests</span>
              <span className="flex items-center gap-1"><span className="w-2 h-0.5 bg-red-500 rounded" /> blocked</span>
              <span className="flex items-center gap-1"><span className="w-2 h-0.5 bg-amber-500 rounded" /> anomalous</span>
            </div>
          </div>
          <TrafficChart data={series} loading={seriesLoading && !series} />
        </section>

        {/* Status + Top Paths */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <section className="rounded-xl border border-gray-800 bg-gray-900/50 p-5">
            <h2 className="text-xs font-mono uppercase tracking-widest text-gray-500 mb-4">Response Status Distribution</h2>
            <StatusDistribution summary={summary} loading={summaryLoading && !summary} />
          </section>
          <section className="rounded-xl border border-gray-800 bg-gray-900/50 p-5">
            <h2 className="text-xs font-mono uppercase tracking-widest text-gray-500 mb-4">Top Paths by Request Volume</h2>
            <TopPaths summary={summary} loading={summaryLoading && !summary} />
          </section>
        </div>

        {/* Live Event Log */}
        <section className="rounded-xl border border-gray-800 bg-gray-900/50 p-5">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-xs font-mono uppercase tracking-widest text-gray-500">
              Live Request Log <span className="ml-2 text-gray-700">/ last 100 events</span>
            </h2>
            <div className="flex items-center gap-3 text-[10px] font-mono text-gray-600">
              <span className="flex items-center gap-1">
                <span className="w-1.5 h-1.5 rounded-sm bg-red-900/50 border border-red-500/20" /> rate-limited
              </span>
              <span className="flex items-center gap-1">
                <span className="w-1.5 h-1.5 rounded-sm bg-amber-900/50 border border-amber-500/20" /> anomalous
              </span>
            </div>
          </div>
          <EventsTable events={events} loading={eventsLoading && !events} />
        </section>

        <footer className="text-center text-[10px] font-mono text-gray-800 pb-4">
          API SENTINEL · built with Go + Redis + React · MIT License
        </footer>
      </main>
    </div>
  );
}
