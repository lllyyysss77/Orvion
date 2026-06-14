"use client"

import { useState, useEffect, memo, useCallback, useMemo, useRef } from "react";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import Loading from "@/components/loading";
import {
  getMetricsSummary,
  getAuthKeySummary,
  getRequestAmountTrend,
  getModelUsageSummary,
  getDailyModelCostTrend
} from "@/lib/api";
import type { AuthKeySummary, DailyModelCostSummary, MetricsSummary, ModelUsageSummaryItem, RequestAmountSummary } from "@/lib/api";
import { getStoredAuthTokenMode, setStoredAuthTokenMode } from "@/lib/auth";
import { toast } from "sonner";
import { resolveModelIcon } from "@/lib/model-icon";
import {
  RefreshCw,
  Activity,
  MessageSquare,
  Clock,
  BadgeCheck,
  ArrowDownToLine,
  ArrowUpToLine,
  CalendarDays,
  Coins,
  CheckCircle2,
  XCircle,
  type LucideIcon,
} from "lucide-react";

const cardHoverClass =
  "transition-all duration-200 ease-out will-change-transform hover:-translate-y-0.5 hover:shadow-md hover:border-primary/30";

const summaryCardClass =
  "relative min-h-[64px] overflow-hidden rounded-[20px] border border-border/50 bg-card/80 shadow-[0_6px_18px_rgba(0,0,0,0.08)] backdrop-blur-sm";

const summarySideTitleClass =
  "text-[9px] font-semibold tracking-[0.22em] text-muted-foreground/80";

const summaryTitleIconClass =
  "size-7 rounded-lg bg-emerald-50 text-emerald-700 flex items-center justify-center shrink-0 ring-1 ring-emerald-100/80 dark:bg-emerald-400/10 dark:text-emerald-200 dark:ring-emerald-400/20";

const summaryMetricIconClass =
  "size-6 rounded-lg bg-emerald-50 text-emerald-700 flex items-center justify-center shrink-0 ring-1 ring-emerald-100/80 dark:bg-emerald-400/10 dark:text-emerald-200 dark:ring-emerald-400/20";

const formatTokenCompact = (value: number | null | undefined) => {
  if (value == null || !Number.isFinite(value)) {
    return "--";
  }
  const abs = Math.abs(value);
  const compact = (divisor: number, suffix: string) => {
    const normalized = value / divisor;
    const normalizedAbs = Math.abs(normalized);
    const digits = normalizedAbs >= 100 ? 0 : normalizedAbs >= 10 ? 1 : 2;
    if (digits === 0) {
      return `${normalized.toFixed(0)}${suffix}`;
    }
    return `${normalized.toFixed(digits).replace(/\.?0+$/, "")}${suffix}`;
  };
  if (abs >= 1_000_000_000_000) return compact(1_000_000_000_000, "T");
  if (abs >= 1_000_000_000) return compact(1_000_000_000, "B");
  if (abs >= 1_000_000) return compact(1_000_000, "M");
  if (abs >= 1_000) return compact(1_000, "K");
  return Math.round(value).toLocaleString();
};

const formatMoney = (value: number | null | undefined, digits?: number) => {
  if (value == null || !Number.isFinite(value)) {
    return "--";
  }
  if (typeof digits === "number") {
    return `$${value.toFixed(digits)}`;
  }
  const abs = Math.abs(value);
  if (abs === 0) return "$0";
  if (abs >= 1) return `$${value.toFixed(2).replace(/\.?0+$/, "")}`;
  if (abs >= 0.01) return `$${value.toFixed(4).replace(/\.?0+$/, "")}`;
  return `$${value.toFixed(6).replace(/\.?0+$/, "")}`;
};

const formatDate = (value: string | null | undefined) => {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toISOString().slice(0, 10);
};

const formatSeconds = (valueMs: number | null | undefined) => {
  if (valueMs == null || !Number.isFinite(valueMs)) {
    return "--";
  }
  return `${(valueMs / 1000).toFixed(2)} s`;
};


// Animated counter component
const AnimatedCounter = ({
  value,
  duration = 1000,
  className,
  formatter,
}: {
  value: number;
  duration?: number;
  className?: string;
  formatter?: (value: number) => string;
}) => {
  const [count, setCount] = useState(0);

  useEffect(() => {
    let startTime: number | null = null;
    const animateCount = (timestamp: number) => {
      if (!startTime) startTime = timestamp;
      const progress = timestamp - startTime;
      const progressRatio = Math.min(progress / duration, 1);
      const currentValue = Math.floor(progressRatio * value);

      setCount(currentValue);

      if (progress < duration) {
        requestAnimationFrame(animateCount);
      }
    };

    requestAnimationFrame(animateCount);
  }, [value, duration]);

  return (
    <div className={["text-3xl font-bold", className].filter(Boolean).join(" ")}>
      {formatter ? formatter(count) : count.toLocaleString()}
    </div>
  );
};

type SummaryMetric = {
  label: string;
  value: React.ReactNode;
  subLabel?: string;
  icon: LucideIcon;
};

type SummaryCardProps = {
  title: string;
  icon: LucideIcon;
  items: [SummaryMetric, SummaryMetric];
};

const SummaryCard = memo(({ title, icon: TitleIcon, items }: SummaryCardProps) => {
  return (
    <Card className={summaryCardClass}>
      <div className="flex h-full">
        <div className="w-11 shrink-0 flex flex-col items-center justify-center gap-1 py-1">
          <span className={summaryTitleIconClass} aria-hidden="true">
            <TitleIcon className="size-3.5" />
          </span>
          <span
            className={summarySideTitleClass}
            style={{ writingMode: "vertical-rl" }}
          >
            {title}
          </span>
        </div>
        <div className="w-px bg-border/60 my-1.5" />
        <div className="flex-1 grid grid-rows-2 gap-0.5 px-1.5 py-0.5">
          {items.map((item) => (
            <div key={item.label} className="flex items-center gap-2">
              <span className={summaryMetricIconClass} aria-hidden="true">
                <item.icon className="size-3" />
              </span>
              <div className="min-w-0">
                <div className="text-[10px] text-muted-foreground">{item.label}</div>
                <div className="text-[13px] font-semibold leading-tight">{item.value}</div>
                {item.subLabel && (
                  <div className="text-[9px] text-muted-foreground">
                    {item.subLabel}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
});

type TodayAmountTrendCardProps = {
  totalRequests: number;
  totalAmount: number;
  rangeLabel: string;
  rangeValue: string;
  curvePoints: RequestAmountPointView[];
};

type RequestAmountPointView = {
  hour: number;
  requests: number;
  amount: number;
};

const buildSmoothLine = (points: { x: number; y: number }[]) => {
  if (points.length < 2) return "";
  if (points.length === 2) {
    return `M${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)} L${points[1].x.toFixed(2)} ${points[1].y.toFixed(2)}`;
  }
  const tension = 1;
  const path: string[] = [`M${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`];
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i - 1] ?? points[i];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[i + 2] ?? p2;

    const cp1x = p1.x + (p2.x - p0.x) / 6 * tension;
    const cp1y = p1.y + (p2.y - p0.y) / 6 * tension;
    const cp2x = p2.x - (p3.x - p1.x) / 6 * tension;
    const cp2y = p2.y - (p3.y - p1.y) / 6 * tension;

    path.push(
      `C${cp1x.toFixed(2)} ${cp1y.toFixed(2)} ${cp2x.toFixed(2)} ${cp2y.toFixed(2)} ${p2.x.toFixed(2)} ${p2.y.toFixed(2)}`
    );
  }
  return path.join(" ");
};

type DailyModelCostCardProps = {
  trend: DailyModelCostSummary;
};

type ModelVisual = {
  color: string;
  iconSrc?: string;
  iconAlt: string;
  typeLabel: string;
};

const hashStringFNV1a = (value: string) => {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i += 1) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
};

const getStableModelColor = (modelName: string) => {
  const hash = hashStringFNV1a(modelName);
  const hue = hash % 360;
  const saturation = 62 + ((hash >>> 9) % 18); // 62% - 79%
  const lightness = 46 + ((hash >>> 17) % 16); // 46% - 61%
  return `hsl(${hue} ${saturation}% ${lightness}%)`;
};

const getModelVisual = (model: string): ModelVisual => {
  const normalized = model.trim().toLowerCase();
  if (!normalized) {
    return {
      color: "#94A3B8",
      iconAlt: "Unknown",
      typeLabel: "未知",
    };
  }
  if (normalized === "others") {
    return {
      color: "#64748B",
      iconAlt: "Others",
      typeLabel: "其他模型",
    };
  }
  const stableColor = getStableModelColor(normalized);
  const matchedIcon = resolveModelIcon(normalized);
  if (matchedIcon) {
    return {
      color: stableColor,
      iconSrc: matchedIcon.src,
      iconAlt: matchedIcon.alt,
      typeLabel: matchedIcon.alt,
    };
  }
  return {
    color: stableColor,
    iconAlt: "Model",
    typeLabel: "通用模型",
  };
};

const formatModelDisplayName = (model: string) => {
  const name = (model || "").trim();
  if (!name) return "unknown";
  if (name.toLowerCase() === "others") return "其他模型";
  return name;
};

const TodayAmountTrendCard = memo(({
  totalRequests,
  totalAmount,
  rangeLabel,
  rangeValue,
  curvePoints,
}: TodayAmountTrendCardProps) => {
  const chartWidth = 640;
  const chartHeight = 260;
  const margin = { top: 24, right: 24, bottom: 34, left: 44 };
  const plotWidth = chartWidth - margin.left - margin.right;
  const plotHeight = chartHeight - margin.top - margin.bottom;
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);

  const requestData = useMemo(
    () => curvePoints.map((point) => point.requests),
    [curvePoints]
  );
  const amountData = useMemo(
    () => curvePoints.map((point) => point.amount),
    [curvePoints]
  );
  const maxRequests = Math.max(1, ...requestData);
  const maxAmount = Math.max(0, ...amountData);
  const pointStep = plotWidth / Math.max(1, curvePoints.length - 1);
  const currentHour = new Date().getHours();
  const currentIndex = curvePoints.findIndex((point) => point.hour === currentHour);
  const peakIndex = requestData.reduce((bestIndex, value, index) => (
    value > requestData[bestIndex] ? index : bestIndex
  ), 0);
  const requestTicks = useMemo(() => {
    const middle = Math.ceil(maxRequests / 2);
    return Array.from(new Set([0, middle, maxRequests])).sort((left, right) => left - right);
  }, [maxRequests]);

  const requestLinePoints = useMemo(
    () => requestData.map((value, index) => {
      const x = margin.left + (curvePoints.length <= 1 ? plotWidth / 2 : pointStep * index);
      const y = margin.top + plotHeight - (value / maxRequests) * plotHeight;
      return { x, y };
    }),
    [curvePoints.length, margin.left, margin.top, maxRequests, plotHeight, plotWidth, pointStep, requestData]
  );
  const amountLinePoints = useMemo(
    () => amountData.map((value, index) => {
      const x = margin.left + (curvePoints.length <= 1 ? plotWidth / 2 : pointStep * index);
      const y = margin.top + plotHeight - (maxAmount <= 0 ? 0 : (value / maxAmount) * plotHeight);
      return { x, y };
    }),
    [amountData, curvePoints.length, margin.left, margin.top, maxAmount, plotHeight, plotWidth, pointStep]
  );
  const requestLine = useMemo(
    () => buildSmoothLine(requestLinePoints),
    [requestLinePoints]
  );
  const requestArea = requestLine && requestLinePoints.length > 0
    ? `${requestLine} L ${requestLinePoints[requestLinePoints.length - 1].x.toFixed(2)} ${margin.top + plotHeight} L ${requestLinePoints[0].x.toFixed(2)} ${margin.top + plotHeight} Z`
    : "";
  const amountLine = useMemo(
    () => (maxAmount > 0 ? buildSmoothLine(amountLinePoints) : ""),
    [amountLinePoints, maxAmount]
  );
  const safeHoveredIndex = hoveredIndex == null || curvePoints.length === 0
    ? null
    : Math.max(0, Math.min(hoveredIndex, curvePoints.length - 1));
  const hoveredRequestPoint = safeHoveredIndex == null ? null : requestLinePoints[safeHoveredIndex];
  const hoveredAmountPoint = safeHoveredIndex == null ? null : amountLinePoints[safeHoveredIndex];
  const hoveredData = safeHoveredIndex == null ? null : curvePoints[safeHoveredIndex];
  const tooltipLeft = hoveredRequestPoint ? `${(hoveredRequestPoint.x / chartWidth) * 100}%` : "0%";
  const tooltipTranslate = safeHoveredIndex == null
    ? "-50%"
    : safeHoveredIndex <= 3
      ? "0"
      : safeHoveredIndex >= curvePoints.length - 4
        ? "-100%"
        : "-50%";
  const peakPoint = totalRequests > 0 ? curvePoints[peakIndex] : null;
  const currentPoint = currentIndex >= 0 ? curvePoints[currentIndex] : null;

  return (
    <Card className={`${cardHoverClass} gap-3 h-full min-h-[460px]`}>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="text-xs text-muted-foreground">24 小时请求分布</div>
            <div className="mt-1 flex items-baseline gap-2">
              <span className="text-2xl font-semibold leading-none text-foreground">{formatCompactValue(totalRequests)}</span>
              <span className="text-xs text-muted-foreground">次请求</span>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3">
            <div className="rounded-lg border border-border/60 bg-muted/30 px-2.5 py-1.5">
              <div className="text-muted-foreground">今日消耗</div>
              <div className="font-semibold text-foreground">{formatMoney(totalAmount)}</div>
            </div>
            <div className="rounded-lg border border-border/60 bg-muted/30 px-2.5 py-1.5">
              <div className="text-muted-foreground">峰值时段</div>
              <div className="font-semibold text-foreground">
                {peakPoint ? `${peakPoint.hour.toString().padStart(2, "0")}:00` : "--"}
              </div>
            </div>
            <div className="rounded-lg border border-border/60 bg-muted/30 px-2.5 py-1.5">
              <div className="text-muted-foreground">{rangeLabel}</div>
              <div className="font-semibold text-foreground">{rangeValue}</div>
            </div>
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-0 flex-1">
        <div className="relative flex h-full flex-col overflow-hidden rounded-2xl border border-border/40 bg-card/60 px-4 py-3">
          <div className="relative z-10 flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
            <div className="flex items-center gap-3">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-2 w-2 rounded-full bg-emerald-500" />
                请求量
              </span>
              <span className="inline-flex items-center gap-1.5">
                <span className="h-0.5 w-4 rounded-full bg-amber-500" />
                金额趋势
              </span>
            </div>
            {currentPoint && (
              <span>
                当前 {currentPoint.hour.toString().padStart(2, "0")}:00 · {currentPoint.requests.toLocaleString()}
              </span>
            )}
          </div>
          <div className="mt-2 flex-1">
            <svg
              viewBox={`0 0 ${chartWidth} ${chartHeight}`}
              className="h-full min-h-[270px] w-full"
              onMouseLeave={() => setHoveredIndex(null)}
              onMouseMove={(event) => {
                if (curvePoints.length <= 1) return;
                const rect = event.currentTarget.getBoundingClientRect();
                if (rect.width <= 0) return;
                const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width));
                const index = Math.round(ratio * (curvePoints.length - 1));
                setHoveredIndex(index);
              }}
            >
              <defs>
                <linearGradient id="requestCurveFill" x1="0" x2="0" y1="0" y2="1">
                  <stop offset="0%" stopColor="#10b981" stopOpacity="0.22" />
                  <stop offset="70%" stopColor="#38bdf8" stopOpacity="0.08" />
                  <stop offset="100%" stopColor="#38bdf8" stopOpacity="0" />
                </linearGradient>
                <linearGradient id="requestCurveStroke" x1="0" x2="1" y1="0" y2="0">
                  <stop offset="0%" stopColor="#10b981" />
                  <stop offset="100%" stopColor="#0ea5e9" />
                </linearGradient>
              </defs>
              {requestTicks.map((tick) => {
                const y = margin.top + plotHeight - (tick / maxRequests) * plotHeight;
                return (
                  <g key={`request-tick-${tick}`}>
                    <line
                      x1={margin.left}
                      y1={y}
                      x2={margin.left + plotWidth}
                      y2={y}
                      className="stroke-border/45"
                      strokeDasharray={tick === 0 ? "0" : "4 6"}
                    />
                    <text
                      x={margin.left - 10}
                      y={y + 4}
                      textAnchor="end"
                      className="fill-muted-foreground text-[11px]"
                    >
                      {formatCompactValue(tick)}
                    </text>
                  </g>
                );
              })}

              {currentIndex >= 0 && requestLinePoints[currentIndex] && (
                <rect
                  x={requestLinePoints[currentIndex].x - Math.max(6, pointStep / 2)}
                  y={margin.top}
                  width={Math.max(12, pointStep)}
                  height={plotHeight}
                  className="fill-sky-500/10"
                />
              )}

              {safeHoveredIndex != null && hoveredRequestPoint && (
                <rect
                  x={hoveredRequestPoint.x - Math.max(6, pointStep / 2)}
                  y={margin.top}
                  width={Math.max(12, pointStep)}
                  height={plotHeight}
                  className="fill-emerald-500/10"
                />
              )}

              {requestArea && (
                <path d={requestArea} fill="url(#requestCurveFill)" />
              )}

              {requestLine && (
                <path
                  d={requestLine}
                  stroke="url(#requestCurveStroke)"
                  fill="none"
                  strokeWidth="3"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              )}

              {amountLine && (
                <path
                  d={amountLine}
                  className="stroke-amber-500"
                  fill="none"
                  strokeWidth="2"
                  strokeDasharray="6 6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  opacity="0.82"
                />
              )}

              {totalRequests > 0 && requestLinePoints[peakIndex] && (
                <circle
                  cx={requestLinePoints[peakIndex].x}
                  cy={requestLinePoints[peakIndex].y}
                  r="4"
                  className="fill-background stroke-emerald-500"
                  strokeWidth="2.5"
                />
              )}

              {currentIndex >= 0 && requestLinePoints[currentIndex] && (
                <circle
                  cx={requestLinePoints[currentIndex].x}
                  cy={requestLinePoints[currentIndex].y}
                  r="3.5"
                  className="fill-sky-500 stroke-card"
                  strokeWidth="2"
                />
              )}

              {hoveredRequestPoint && (
                <>
                  <line
                    x1={hoveredRequestPoint.x}
                    y1={margin.top}
                    x2={hoveredRequestPoint.x}
                    y2={margin.top + plotHeight}
                    className="stroke-emerald-500/45"
                    strokeDasharray="4 4"
                  />
                  <circle
                    cx={hoveredRequestPoint.x}
                    cy={hoveredRequestPoint.y}
                    r="4.6"
                    className="fill-emerald-500 stroke-card"
                    strokeWidth="2"
                  />
                  {hoveredAmountPoint && amountLine && (
                    <circle
                      cx={hoveredAmountPoint.x}
                      cy={hoveredAmountPoint.y}
                      r="3.2"
                      className="fill-amber-500 stroke-card"
                      strokeWidth="2"
                    />
                  )}
                </>
              )}

              {[0, 6, 12, 18, 23].map((hour) => {
                const index = Math.min(curvePoints.length - 1, Math.max(0, hour));
                const x = margin.left + (curvePoints.length <= 1 ? plotWidth / 2 : pointStep * index);
                return (
                  <text
                    key={`hour-label-${hour}`}
                    x={x}
                    y={chartHeight - 8}
                    textAnchor="middle"
                    className="fill-muted-foreground text-[11px]"
                  >
                    {hour}
                  </text>
                );
              })}
            </svg>
          </div>
          {hoveredData && (
            <div
              className="pointer-events-none absolute top-20 z-10 rounded-lg border border-border/70 bg-card/95 px-2.5 py-2 text-[11px] shadow-lg backdrop-blur"
              style={{ left: tooltipLeft, transform: `translateX(${tooltipTranslate})` }}
            >
              <div className="font-semibold text-foreground">{hoveredData.hour.toString().padStart(2, "0")}:00</div>
              <div className="mt-1 text-muted-foreground">请求 {hoveredData.requests.toLocaleString()}</div>
              <div className="text-muted-foreground">金额 ${formatAmountValue(hoveredData.amount)}</div>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
});

const DailyModelCostCard = memo(({ trend }: DailyModelCostCardProps) => {
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const chartWidth = 640;
  const chartHeight = 320;
  const margin = { top: 14, right: 16, bottom: 36, left: 58 };
  const plotWidth = chartWidth - margin.left - margin.right;
  const plotHeight = chartHeight - margin.top - margin.bottom;

  const seriesWithVisual = useMemo(
    () => trend.series.map((item) => ({
      ...item,
      visual: getModelVisual(item.model),
    })),
    [trend.series]
  );

  const axisMax = DAILY_MODEL_COST_AXIS_MAX;
  const yTicks = DAILY_MODEL_COST_Y_TICKS;

  const groupWidth = trend.labels.length > 0 ? plotWidth / trend.labels.length : plotWidth;
  const barWidth = Math.min(44, Math.max(16, groupWidth * 0.58));

  const getY = (value: number) => margin.top + plotHeight - (value / axisMax) * plotHeight;

  const tooltip = useMemo(() => {
    if (hoveredIndex == null) return null;
    if (hoveredIndex < 0 || hoveredIndex >= trend.labels.length) return null;
    const breakdown = seriesWithVisual
      .map((item) => ({
        model: formatModelDisplayName(item.model),
        amount: item.amounts[hoveredIndex] ?? 0,
        visual: item.visual,
      }))
      .filter((item) => item.amount > 0)
      .sort((a, b) => b.amount - a.amount);

    return {
      label: trend.labels[hoveredIndex],
      total: trend.totals[hoveredIndex] ?? 0,
      breakdown,
      exceedAxisMax: (trend.totals[hoveredIndex] ?? 0) > axisMax,
    };
  }, [axisMax, hoveredIndex, seriesWithVisual, trend.labels, trend.totals]);

  return (
    <Card className={`${cardHoverClass} gap-3 h-full min-h-[460px]`}>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">每日模型成本</span>
          </div>
          <div className="text-xs text-muted-foreground">
            最近 <span className="text-foreground">{trend.labels.length}</span> 天
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-0 flex-1">
        {seriesWithVisual.length === 0 ? (
          <div className="flex h-full items-center justify-center rounded-2xl border border-dashed border-border/50 px-4 py-10 text-center text-xs text-muted-foreground">
            暂无模型成本数据
          </div>
        ) : (
          <div className="relative flex h-full flex-col overflow-hidden rounded-2xl border border-border/40 bg-card/60 px-3 py-3">
            <div className="pointer-events-none absolute inset-0 opacity-20" style={{ backgroundImage: "radial-gradient(circle at 20% 10%, rgba(59,130,246,0.25), transparent 60%)" }} />
            <div className="relative z-10 flex flex-col">
              <svg
                viewBox={`0 0 ${chartWidth} ${chartHeight}`}
                className="h-[290px] w-full"
              >
                {yTicks.map((tick) => {
                  const y = getY(tick);
                  return (
                    <g key={`y-${tick}`}>
                      <line
                        x1={margin.left}
                        y1={y}
                        x2={margin.left + plotWidth}
                        y2={y}
                        className="stroke-border/45"
                      />
                      <text
                        x={margin.left - 8}
                        y={y + 4}
                        textAnchor="end"
                        className="fill-muted-foreground text-[12px]"
                      >
                        {formatAxisTick(tick)}
                      </text>
                    </g>
                  );
                })}

                <line
                  x1={margin.left}
                  y1={margin.top}
                  x2={margin.left}
                  y2={margin.top + plotHeight}
                  className="stroke-border/65"
                />
                <line
                  x1={margin.left}
                  y1={margin.top + plotHeight}
                  x2={margin.left + plotWidth}
                  y2={margin.top + plotHeight}
                  className="stroke-border/65"
                />

                <text
                  transform={`translate(${18} ${margin.top + plotHeight / 2}) rotate(-90)`}
                  textAnchor="middle"
                  className="fill-muted-foreground text-[13px]"
                >
                  费用 ($)
                </text>

                {trend.labels.map((label, index) => {
                  const slotX = margin.left + groupWidth * index;
                  const x = slotX + (groupWidth - barWidth) / 2;
                  let offset = 0;

                  return (
                    <g key={`${label}-${index}`}>
                      {seriesWithVisual.map((item) => {
                        const amount = item.amounts[index] ?? 0;
                        if (amount <= 0) return null;
                        const remain = Math.max(0, axisMax - offset);
                        const drawAmount = Math.min(amount, remain);
                        if (drawAmount <= 0) {
                          offset += amount;
                          return null;
                        }
                        const y = getY(offset + drawAmount);
                        const height = Math.max((drawAmount / axisMax) * plotHeight, 1);
                        offset += amount;
                        return (
                          <rect
                            key={`${item.model}-${index}`}
                            x={x}
                            y={y}
                            width={barWidth}
                            height={height}
                            rx={4}
                            fill={item.visual.color}
                            fillOpacity={0.92}
                          />
                        );
                      })}

                      <rect
                        x={slotX}
                        y={margin.top}
                        width={groupWidth}
                        height={plotHeight}
                        fill="transparent"
                        onMouseEnter={() => setHoveredIndex(index)}
                        onFocus={() => setHoveredIndex(index)}
                        onMouseLeave={() => setHoveredIndex(null)}
                        onBlur={() => setHoveredIndex(null)}
                      />

                      <text
                        x={slotX + groupWidth / 2}
                        y={margin.top + plotHeight + 18}
                        textAnchor="middle"
                        className="fill-muted-foreground text-[13px]"
                      >
                        {label}
                      </text>
                    </g>
                  );
                })}
              </svg>

              <div className="mt-1 shrink-0 flex flex-wrap items-center justify-center gap-x-3 gap-y-1.5 text-[12px] font-medium text-foreground/85">
                {seriesWithVisual.map((item) => (
                  <span key={item.model} className="inline-flex items-center gap-1.5">
                    <span
                      className="inline-block size-2.5 rounded-sm"
                      style={{ backgroundColor: item.visual.color }}
                    />
                    {formatModelDisplayName(item.model)}
                  </span>
                ))}
              </div>
            </div>

            {tooltip && tooltip.breakdown.length > 0 && (
              <div className="pointer-events-none absolute right-3 top-3 z-20 w-52 rounded-xl border border-border/70 bg-card/95 px-3 py-2 text-xs shadow-md backdrop-blur-sm">
                <div className="font-semibold text-foreground">{tooltip.label}</div>
                <div className="mt-1 text-muted-foreground">Total: {formatMoney(tooltip.total, 4)}</div>
                {tooltip.exceedAxisMax && (
                  <div className="mt-1 text-[10px] text-amber-600">
                    超过坐标上限 $60，柱体已封顶显示
                  </div>
                )}
                <div className="mt-2 space-y-1">
                  {tooltip.breakdown.map((item) => (
                    <div key={item.model} className="flex items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5 min-w-0">
                        <span className="size-2 rounded-sm shrink-0" style={{ backgroundColor: item.visual.color }} />
                        <span className="truncate">{item.model}</span>
                      </span>
                      <span className="font-medium text-foreground">${formatAmountValue(item.amount)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
});

type ModelUsageCardProps = {
  items: ModelUsageSummaryItem[];
};

const ModelUsageCard = memo(({ items }: ModelUsageCardProps) => {
  const totalTokens = items.reduce((sum, item) => sum + item.total_tokens, 0);
  const totalCost = items.reduce((sum, item) => sum + item.total_cost, 0);

  return (
    <Card className={`${cardHoverClass} gap-3`}>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">模型用量概览</span>
            <div className="flex flex-wrap items-center gap-4 text-xs text-muted-foreground">
              <span>模型数 <span className="text-foreground">{items.length}</span></span>
              <span>总 Tokens <span className="text-foreground">{formatTokenCompact(totalTokens)}</span></span>
              <span>总费用 <span className="text-foreground">{formatMoney(totalCost)}</span></span>
            </div>
          </div>
          <span className="text-[11px] text-muted-foreground">按模型自动识别图标与颜色</span>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        {items.length === 0 ? (
          <div className="flex items-center justify-center rounded-2xl border border-dashed border-border/50 px-4 py-10 text-center text-xs text-muted-foreground">
            暂无模型用量数据
          </div>
        ) : (
          <div className="rounded-2xl border border-border/40 bg-card/60 p-2">
            <div className="max-h-[360px] space-y-2 overflow-y-auto pr-1">
              {items.map((item, index) => {
                const visual = getModelVisual(item.model);
                return (
                  <div
                    key={item.model}
                    className="rounded-2xl border border-border/50 bg-background/80 px-3 py-3 shadow-sm"
                  >
                    <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                      <div className="min-w-0 flex items-center gap-3">
                        <div
                          className="flex size-10 shrink-0 items-center justify-center rounded-xl border bg-muted/60 shadow-sm"
                          style={{ borderColor: `${visual.color}55` }}
                        >
                          {visual.iconSrc ? (
                            <img src={visual.iconSrc} alt={visual.iconAlt} className="size-5" />
                          ) : (
                            <span className="text-xs font-semibold text-muted-foreground">
                              {formatModelDisplayName(item.model).slice(0, 2).toUpperCase()}
                            </span>
                          )}
                        </div>
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <span
                              className="rounded-full border border-border/70 bg-background/80 px-2 py-0.5 text-[10px] font-semibold tracking-[0.14em] text-muted-foreground"
                            >
                              #{String(index + 1).padStart(2, "0")}
                            </span>
                            <span className="size-2 rounded-sm shrink-0" style={{ backgroundColor: visual.color }} />
                            <div className="truncate text-sm font-semibold text-foreground">
                              {formatModelDisplayName(item.model)}
                            </div>
                          </div>
                          <div className="mt-1 text-[11px] text-muted-foreground">{visual.typeLabel}</div>
                        </div>
                      </div>
                      <div className="grid grid-cols-2 gap-2 sm:min-w-[260px]">
                        <div className="rounded-xl border border-border/60 bg-background/80 px-3 py-2">
                          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                            <ArrowDownToLine className="size-3.5" style={{ color: visual.color }} />
                            <span>Tokens</span>
                          </div>
                          <div className="mt-1 text-sm font-semibold text-foreground">
                            {formatTokenCompact(item.total_tokens)}
                          </div>
                        </div>
                        <div className="rounded-xl border border-border/60 bg-background/80 px-3 py-2">
                          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                            <Coins className="size-3.5" style={{ color: visual.color }} />
                            <span>费用</span>
                          </div>
                          <div className="mt-1 text-sm font-semibold text-foreground">
                            {formatMoney(item.total_cost)}
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
});

type AuthKeyDashboardProps = {
  summary: AuthKeySummary | null;
  errorMessage?: string | null;
};

const AuthKeyDashboard = ({ summary, errorMessage }: AuthKeyDashboardProps) => {
  if (!summary && !errorMessage) {
    return (
      <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
        <CardContent className="py-6 text-sm text-muted-foreground">
          正在加载 API Key 概览...
        </CardContent>
      </Card>
    );
  }

  const name = summary?.name?.trim() || "未命名";
  const keyMasked = summary?.keyMasked || "--";
  const expiresAt = formatDate(summary?.expiresAt);
  const expireInDays = summary?.expireInDays;
  const expireText = summary?.expiresAt
    ? `${expireInDays ?? 0} 天后到期`
    : "长期有效";
  const totalCost = summary?.totalCost ?? 0;
  const costMax = Math.max(totalCost, 30);
  const costProgress = costMax > 0 ? Math.min(totalCost / costMax, 1) : 0;
  const allowedModels = summary?.allowAll ? ["全部模型"] : (summary?.models || []);

  return (
    <div className="space-y-4">
      {errorMessage ? (
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="py-6 text-sm text-muted-foreground">
            {errorMessage}
          </CardContent>
        </Card>
      ) : null}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1.2fr_1fr]">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div className="rounded-xl border border-border/60 bg-muted/30 px-3 py-2">
                <div className="text-xs text-muted-foreground">名称</div>
                <div className="mt-1 text-sm font-semibold text-foreground">{name}</div>
              </div>
              <div className="rounded-xl border border-border/60 bg-muted/30 px-3 py-2">
                <div className="text-xs text-muted-foreground">过期时间</div>
                <div className="mt-1 text-sm font-semibold text-foreground">{expiresAt}</div>
              </div>
            </div>
            <div className="rounded-xl border border-border/60 bg-muted/40 px-3 py-2">
              <div className="text-xs text-muted-foreground">API Key（掩码）</div>
              <div className="mt-1 break-all font-mono text-sm text-foreground">
                {keyMasked}
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
              <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] text-emerald-700">
                {expireText}
              </span>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-start justify-between">
              <div>
                <div className="text-sm text-muted-foreground">消耗费用</div>
                <div className="mt-1 text-4xl font-semibold text-emerald-600">
                  {formatMoney(summary?.totalCost, 2)}
                </div>
              </div>
              <Coins className="size-8 text-muted-foreground/40" />
            </div>
            <div className="space-y-1">
              <div className="h-2 rounded-full bg-muted/50">
                <div
                  className="h-2 rounded-full bg-emerald-500/70"
                  style={{ width: `${costProgress * 100}%` }}
                />
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>0</span>
                <span>∞</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <CheckCircle2 className="size-4 text-emerald-500" />
              成功请求
            </div>
            <div className="text-xl font-semibold">{formatTokenCompact(summary?.successRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <XCircle className="size-4 text-rose-500" />
              失败请求
            </div>
            <div className="text-xl font-semibold">{formatTokenCompact(summary?.failureRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Activity className="size-4 text-emerald-500" />
              请求次数
            </div>
            <div className="text-xl font-semibold">{formatTokenCompact(summary?.totalRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Clock className="size-4 text-amber-500" />
              消耗时间
            </div>
            <div className="text-xl font-semibold">{formatSeconds(summary?.totalTimeMs)}</div>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold">
                <ArrowDownToLine className="size-4 text-emerald-600" />
                消耗 Token
              </div>
              <div className="text-sm font-semibold">
                {formatTokenCompact(summary?.totalTokens)}
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4 text-xs text-muted-foreground">
              <div className="space-y-1">
                <div>输入 Tokens</div>
                <div className="text-base font-semibold text-foreground">
                  {formatTokenCompact(summary?.promptTokens)}
                </div>
              </div>
              <div className="space-y-1">
                <div>输出 Tokens</div>
                <div className="text-base font-semibold text-foreground">
                  {formatTokenCompact(summary?.completionTokens)}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold">
                <Coins className="size-4 text-emerald-600" />
                消耗费用
              </div>
              <div className="text-sm font-semibold">
                {formatMoney(summary?.totalCost)}
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4 text-xs text-muted-foreground">
              <div className="space-y-1">
                <div>输入费用</div>
                <div className="text-base font-semibold text-foreground">
                  {formatMoney(summary?.inputCost)}
                </div>
              </div>
              <div className="space-y-1">
                <div>输出费用</div>
                <div className="text-base font-semibold text-foreground">
                  {formatMoney(summary?.outputCost)}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
        <CardContent className="p-4 space-y-3">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <BadgeCheck className="size-4 text-emerald-600" />
            支持的模型
          </div>
          {allowedModels.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {allowedModels.map((model) => (
                <Badge key={model} variant="secondary" className="rounded-full">
                  {model}
                </Badge>
              ))}
            </div>
          ) : (
            <div className="text-xs text-muted-foreground">暂无模型</div>
          )}
        </CardContent>
      </Card>
    </div>
  );
};

const defaultRequestAmount: RequestAmountSummary = {
  total_requests: 0,
  total_amount: 0,
  range: "today",
  points: [],
};

const defaultDailyModelCost: DailyModelCostSummary = {
  range: "daily",
  dates: [],
  labels: [],
  totals: [],
  series: [],
};

const formatCompactValue = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  const abs = Math.abs(value);
  const format = (num: number, suffix: string) => {
    const fixed = num.toFixed(2).replace(/\.?0+$/, "");
    return `${fixed}${suffix}`;
  };
  if (abs >= 1_000_000) return format(value / 1_000_000, "M");
  if (abs >= 1_000) return format(value / 1_000, "k");
  return value.toString();
};

const formatAmountValue = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  const abs = Math.abs(value);
  if (abs === 0) return "0";
  if (abs >= 1) return value.toFixed(2).replace(/\.?0+$/, "");
  if (abs >= 0.01) return value.toFixed(4).replace(/\.?0+$/, "");
  return value.toFixed(6).replace(/\.?0+$/, "");
};

const DAILY_MODEL_COST_AXIS_MAX = 60;
const DAILY_MODEL_COST_Y_TICKS = [0, 10, 20, 30, 40, 50, 60];

const formatAxisTick = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  return value.toFixed(0);
};

type HomeHeaderProps = {
  title: string;
  onRefresh: () => void;
};

const HomeHeader = memo(({ title, onRefresh }: HomeHeaderProps) => {
  return (
    <div className="flex flex-col gap-2 flex-shrink-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">{title}</h2>
        </div>
        <Button
          onClick={onRefresh}
          variant="outline"
          size="icon"
          className="ml-auto shrink-0"
          aria-label="刷新概览"
          title="刷新概览"
        >
          <RefreshCw className="size-4" />
        </Button>
      </div>
    </div>
  );
});

export default function Home() {
  const [loading, setLoading] = useState(true);
  const [requestAmount, setRequestAmount] = useState<RequestAmountSummary>(defaultRequestAmount);
  const [modelUsage, setModelUsage] = useState<ModelUsageSummaryItem[]>([]);
  const [dailyModelCost, setDailyModelCost] = useState<DailyModelCostSummary>(defaultDailyModelCost);
  const [authKeySummary, setAuthKeySummary] = useState<AuthKeySummary | null>(null);
  const [authKeyError, setAuthKeyError] = useState<string | null>(null);
  const [authKeyMode, setAuthKeyMode] = useState(false);
  const autoRefreshBusyRef = useRef(false);

  // Real data from APIs
  const [summary, setSummary] = useState<MetricsSummary>({
    totalReqs: 0,
    successRate: 0,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    todayTokens: 0,
    totalAmount: 0,
    todayAmount: 0,
    todayReqs: 0,
    todaySuccessRate: 0,
    todaySuccessReqs: 0,
    todayFailureReqs: 0,
    totalSuccessReqs: 0,
    totalFailureReqs: 0,
  });

  const fetchAuthKeySummary = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    try {
      const data = await getAuthKeySummary();
      setAuthKeySummary(data);
      setAuthKeyError(null);
      return true;
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!silent) {
        setAuthKeySummary(null);
        setAuthKeyError(`获取 API Key 概览失败: ${message}`);
      }
      return false;
    }
  }, []);
  const fetchSummary = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    try {
      const data = await getMetricsSummary();
      setSummary(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!silent) {
        toast.error(`获取系统概览失败: ${message}`);
      }
      console.error(err);
    }
  }, []);

  const fetchRequestAmount = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    try {
      const data = await getRequestAmountTrend();
      setRequestAmount(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!silent) {
        toast.error(`获取请求金额趋势失败: ${message}`);
      }
      console.error(err);
      if (!silent) {
        setRequestAmount(defaultRequestAmount);
      }
    }
  }, []);

  const fetchModelUsage = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    try {
      const data = await getModelUsageSummary();
      setModelUsage(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!silent) {
        toast.error(`获取模型用量失败: ${message}`);
      }
      console.error(err);
      if (!silent) {
        setModelUsage([]);
      }
    }
  }, []);

  const fetchDailyModelCost = useCallback(async (options?: { silent?: boolean }) => {
    const silent = options?.silent === true;
    try {
      const data = await getDailyModelCostTrend(7, 10);
      setDailyModelCost(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (!silent) {
        toast.error(`获取每日模型成本失败: ${message}`);
      }
      console.error(err);
      if (!silent) {
        setDailyModelCost(defaultDailyModelCost);
      }
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    const mode = getStoredAuthTokenMode();

    if (mode === "auth_key") {
      setAuthKeyMode(true);
      await fetchAuthKeySummary();
      setLoading(false);
      return;
    }

    if (mode === "admin") {
      setAuthKeyMode(false);
      setAuthKeySummary(null);
      setAuthKeyError(null);
      await Promise.all([fetchSummary(), fetchRequestAmount(), fetchModelUsage(), fetchDailyModelCost()]);
      setLoading(false);
      return;
    }

    // 兼容历史会话：未记录模式时自动探测一次。
    const authKeyAvailable = await fetchAuthKeySummary();
    if (authKeyAvailable) {
      setStoredAuthTokenMode("auth_key");
      setAuthKeyMode(true);
      setLoading(false);
      return;
    }

    setAuthKeyMode(false);
    setAuthKeySummary(null);
    setAuthKeyError(null);
    setStoredAuthTokenMode("admin");
    await Promise.all([fetchSummary(), fetchRequestAmount(), fetchModelUsage(), fetchDailyModelCost()]);
    setLoading(false);
  }, [fetchAuthKeySummary, fetchDailyModelCost, fetchModelUsage, fetchRequestAmount, fetchSummary]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (loading) {
      return;
    }

    const runSilentRefresh = async () => {
      if (autoRefreshBusyRef.current) {
        return;
      }
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        return;
      }

      autoRefreshBusyRef.current = true;
      try {
        if (authKeyMode) {
          await fetchAuthKeySummary({ silent: true });
          return;
        }
        await Promise.allSettled([
          fetchSummary({ silent: true }),
          fetchRequestAmount({ silent: true }),
          fetchModelUsage({ silent: true }),
          fetchDailyModelCost({ silent: true }),
        ]);
      } finally {
        autoRefreshBusyRef.current = false;
      }
    };

    const intervalMs = authKeyMode ? 15_000 : 8_000;
    const timer = window.setInterval(() => {
      void runSilentRefresh();
    }, intervalMs);

    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void runSilentRefresh();
      }
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [authKeyMode, fetchAuthKeySummary, fetchDailyModelCost, fetchModelUsage, fetchRequestAmount, fetchSummary, loading]);

  const requestCurvePoints: RequestAmountPointView[] = requestAmount.points.length > 0
    ? requestAmount.points.map((point) => ({
      hour: point.hour,
      requests: point.requests,
      amount: point.amount,
    }))
    : Array.from({ length: 24 }, (_, hour) => ({
      hour,
      requests: 0,
      amount: 0,
    }));
  const rangeValue = requestAmount.range === "today" ? "今天" : requestAmount.range;

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <HomeHeader
        title={authKeyMode ? "API Key 概览" : "系统概览"}
        onRefresh={() => void load()}
      />

      <div className="flex-1 min-h-0 overflow-y-auto">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message={authKeyMode ? "加载 API Key 概览" : "加载系统概览"} />
          </div>
        ) : authKeyMode ? (
          <AuthKeyDashboard summary={authKeySummary} errorMessage={authKeyError} />
        ) : (
          <div className="space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
              <SummaryCard
                title="请求统计"
                icon={Activity}
                items={[
                  {
                    label: "请求次数",
                    value: <AnimatedCounter value={summary.totalReqs} className="text-base" />,
                    icon: MessageSquare,
                  },
                  {
                    label: "今日请求",
                    value: <AnimatedCounter value={summary.todayReqs} className="text-base" />,
                    icon: Clock,
                  },
                ]}
              />

              <SummaryCard
                title="金额统计"
                icon={Coins}
                items={[
                  {
                    label: "今日消耗金额",
                    value: <span className="text-base font-semibold">{formatMoney(summary.todayAmount)}</span>,
                    icon: CalendarDays,
                  },
                  {
                    label: "总金额",
                    value: <span className="text-base font-semibold">{formatMoney(summary.totalAmount)}</span>,
                    icon: Coins,
                  },
                ]}
              />

              <SummaryCard
                title="令牌统计"
                icon={ArrowDownToLine}
                items={[
                  {
                    label: "今日消耗 Tokens",
                    value: <AnimatedCounter value={summary.todayTokens} className="text-base" formatter={formatTokenCompact} />,
                    icon: CalendarDays,
                  },
                  {
                    label: "总消耗 Tokens",
                    value: <AnimatedCounter value={summary.totalTokens} className="text-base" formatter={formatTokenCompact} />,
                    icon: ArrowUpToLine,
                  },
                ]}
              />

              <SummaryCard
                title="今日统计"
                icon={CalendarDays}
                items={[
                  {
                    label: "今日成功率",
                    value: <span className="text-base font-semibold">{summary.todaySuccessRate.toFixed(2)}%</span>,
                    icon: BadgeCheck,
                  },
                  {
                    label: "今日成功",
                    value: <AnimatedCounter value={summary.todaySuccessReqs} className="text-base" />,
                    subLabel: `失败 ${summary.todayFailureReqs.toLocaleString()}`,
                    icon: ArrowUpToLine,
                  },
                ]}
              />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4 xl:items-stretch">
              <DailyModelCostCard trend={dailyModelCost} />

              <TodayAmountTrendCard
                totalRequests={requestAmount.total_requests}
                totalAmount={requestAmount.total_amount}
                rangeLabel="时间范围"
                rangeValue={rangeValue}
                curvePoints={requestCurvePoints}
              />
            </div>

            <ModelUsageCard items={modelUsage} />

          </div>
        )}
      </div>
    </div>
  );
}
