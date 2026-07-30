"use client";

import { useEffect, useState } from "react";
import { DollarSign, TrendingUp, AlertTriangle, CheckCircle } from "lucide-react";
import { api } from "@/lib/api";
import { formatBytes, formatDate } from "@/lib/utils";
import type { CostBreakdown, CostAlert } from "@/types";

export default function CostsPage() {
  const [breakdown, setBreakdown] = useState<CostBreakdown | null>(null);
  const [alerts, setAlerts] = useState<CostAlert[]>([]);
  const [period, setPeriod] = useState(() => {
    const now = new Date();
    return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`;
  });

  useEffect(() => {
    loadData();
  }, [period]);

  const loadData = () => {
    api.costs.getBreakdown(period).then(setBreakdown).catch(console.error);
    api.costs.getAlerts(false).then(setAlerts).catch(console.error);
  };

  const handleResolveAlert = async (alertId: string) => {
    try {
      await api.costs.resolveAlert(alertId);
      loadData();
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : "Failed to resolve alert");
    }
  };

  const formatCost = (cost: number) => {
    return new Intl.NumberFormat("en-US", {
      style: "currency",
      currency: "USD",
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(cost);
  };

  const getSeverityColor = (severity: string) => {
    switch (severity) {
      case "critical":
        return "text-red-400 border-red-900/50 bg-red-900/20";
      case "warning":
        return "text-amber-400 border-amber-900/50 bg-amber-900/20";
      default:
        return "text-blue-400 border-blue-900/50 bg-blue-900/20";
    }
  };

  if (!breakdown) {
    return <div className="text-slate-600 text-sm p-8">Loading costs…</div>;
  }

  const computePct = breakdown.total_cost > 0 ? (breakdown.compute_cost / breakdown.total_cost) * 100 : 0;
  const storagePct = breakdown.total_cost > 0 ? (breakdown.storage_cost / breakdown.total_cost) * 100 : 0;
  const networkPct = breakdown.total_cost > 0 ? (breakdown.network_cost / breakdown.total_cost) * 100 : 0;

  return (
    <div className="max-w-6xl mx-auto space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-slate-100">Cost Dashboard</h1>
          <p className="text-sm text-slate-500 mt-1">Resource usage and cost tracking</p>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-xs text-slate-500">Period:</label>
          <input
            type="month"
            value={period}
            onChange={(e) => setPeriod(e.target.value)}
            className="bg-slate-900 border border-slate-700 rounded px-3 py-1.5 text-sm text-slate-200 focus:outline-none focus:border-indigo-500"
          />
        </div>
      </div>

      {/* Total Cost Card */}
      <div className="bg-gradient-to-br from-indigo-900/30 to-purple-900/30 border border-indigo-800/50 rounded-lg p-6">
        <div className="flex items-center gap-3 mb-2">
          <DollarSign className="w-8 h-8 text-indigo-400" />
          <div>
            <div className="text-sm text-slate-400">Total Cost</div>
            <div className="text-3xl font-bold text-slate-100">{formatCost(breakdown.total_cost)}</div>
          </div>
        </div>
        <div className="text-xs text-slate-500 mt-2">{breakdown.period}</div>
      </div>

      {/* Cost Breakdown */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg p-6">
        <h2 className="text-sm font-medium text-slate-400 uppercase tracking-wide mb-4">Cost Breakdown</h2>
        <div className="space-y-4">
          {/* Compute */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-sm text-slate-300">Compute (CPU/Memory/GPU)</span>
              <span className="text-sm font-mono text-slate-400">
                {formatCost(breakdown.compute_cost)} <span className="text-slate-600">({computePct.toFixed(0)}%)</span>
              </span>
            </div>
            <div className="h-2 bg-slate-800 rounded-full overflow-hidden">
              <div
                className="h-full bg-indigo-500 transition-all"
                style={{ width: `${computePct}%` }}
              />
            </div>
          </div>

          {/* Storage */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-sm text-slate-300">Storage (Workspace/Snapshots/Artifacts)</span>
              <span className="text-sm font-mono text-slate-400">
                {formatCost(breakdown.storage_cost)} <span className="text-slate-600">({storagePct.toFixed(0)}%)</span>
              </span>
            </div>
            <div className="h-2 bg-slate-800 rounded-full overflow-hidden">
              <div
                className="h-full bg-emerald-500 transition-all"
                style={{ width: `${storagePct}%` }}
              />
            </div>
          </div>

          {/* Network */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="text-sm text-slate-300">Network (Egress)</span>
              <span className="text-sm font-mono text-slate-400">
                {formatCost(breakdown.network_cost)} <span className="text-slate-600">({networkPct.toFixed(0)}%)</span>
              </span>
            </div>
            <div className="h-2 bg-slate-800 rounded-full overflow-hidden">
              <div
                className="h-full bg-amber-500 transition-all"
                style={{ width: `${networkPct}%` }}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Top Spending Sessions */}
      <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
        <div className="px-6 py-4 border-b border-slate-800">
          <h2 className="text-sm font-medium text-slate-400 uppercase tracking-wide flex items-center gap-2">
            <TrendingUp className="w-4 h-4" /> Top Spending Sessions
          </h2>
        </div>
        {breakdown.top_sessions.length === 0 ? (
          <div className="p-6 text-center text-slate-600 text-sm">No session costs for this period.</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-800 text-xs text-slate-500 uppercase tracking-wide">
                <th className="px-6 py-3 text-left">Session</th>
                <th className="px-6 py-3 text-left">Compute</th>
                <th className="px-6 py-3 text-left">Storage</th>
                <th className="px-6 py-3 text-left">Network</th>
                <th className="px-6 py-3 text-left">Total</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/50">
              {breakdown.top_sessions.map((s) => (
                <tr key={s.session_id} className="hover:bg-slate-800/20">
                  <td className="px-6 py-3 font-mono text-xs text-slate-300">{s.session_name}</td>
                  <td className="px-6 py-3 text-xs text-slate-500">{formatCost(s.compute_cost)}</td>
                  <td className="px-6 py-3 text-xs text-slate-500">{formatCost(s.storage_cost)}</td>
                  <td className="px-6 py-3 text-xs text-slate-500">{formatCost(s.network_cost)}</td>
                  <td className="px-6 py-3 font-mono text-xs text-slate-200 font-medium">
                    {formatCost(s.total_cost)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Alerts */}
      {alerts.length > 0 && (
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-lg overflow-hidden">
          <div className="px-6 py-4 border-b border-slate-800">
            <h2 className="text-sm font-medium text-slate-400 uppercase tracking-wide flex items-center gap-2">
              <AlertTriangle className="w-4 h-4" /> Active Alerts ({alerts.length})
            </h2>
          </div>
          <div className="divide-y divide-slate-800/50">
            {alerts.map((alert) => (
              <div key={alert.id} className="px-6 py-4 hover:bg-slate-800/20">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1">
                    <div className="flex items-center gap-2 mb-1">
                      <span className={`px-2 py-0.5 rounded text-xs font-medium ${getSeverityColor(alert.severity)}`}>
                        {alert.severity}
                      </span>
                      <span className="text-xs text-slate-600">{alert.alert_type}</span>
                    </div>
                    <div className="text-sm text-slate-200 font-medium mb-1">{alert.title}</div>
                    <div className="text-xs text-slate-500">{alert.description}</div>
                    {alert.potential_savings && (
                      <div className="text-xs text-emerald-400 mt-1">
                        💰 Potential savings: {formatCost(alert.potential_savings)}/month
                      </div>
                    )}
                    <div className="text-xs text-slate-600 mt-2">{formatDate(alert.created_at)}</div>
                  </div>
                  <button
                    onClick={() => handleResolveAlert(alert.id)}
                    className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-emerald-400 border border-emerald-900/50 rounded hover:bg-emerald-900/20"
                  >
                    <CheckCircle className="w-3 h-3" /> Resolve
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
